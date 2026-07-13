# rundiff

[English](README.md)

**コマンド出力を「前回の実行」と差分する — `fixed` / `new` / `unchanged` を AI コーディングエージェント向けに。**

fix → test → fix のループで、エージェントは毎回*同じ* 50 KB のテスト出力を読み直し、
目 diff で「何が変わったか」を探す。rundiff はその差分を代わりにやる: コマンドを実行し、
再実行時には**前回から変わった分だけ**を返す。

[`pare`](https://github.com/akira-toriyama/pare) が*空間*方向（1 回の出力を予算内に削る）
に切るのに対し、rundiff は*時間*方向（実行と実行の間）に切る。両者は合成できる。

```console
$ rundiff -- go test ./...          # 初回: ベースラインを確立し、出力をそのまま出す
$ rundiff -- go test ./...          # 再実行: 差分だけ
── delta · fixed  exit=0 (prev 1)  +0 −1 ~214  churn=0.005  age=42s ──
- FAIL: TestParse/negative (0.00s)
```

## ただの `diff` ではない理由

素朴な `diff <(cmd) cache` は実際のコマンド出力では役に立たない: タイムスタンプ・所要時間・
temp path・ANSI 色・**テストの実行順**が毎回変わるので、全部が違って見える。rundiff の差分は:

- **順序非依存。** 行の*多重集合*を比較するので、テストランナーが毎回違う順で出力しても
  **変化ゼロ**と報告する。ただ移動しただけの行は unchanged 扱い。
- **正規化済み。** タイムスタンプ・所要時間・temp path（`/tmp/…`, `/var/folders/…`）・
  ANSI エスケープ・goroutine id・UUID・ループバックの `host:port` を比較*前*に正規化するので、ノイズとして
  出てこない。実データになりうるもの（素の数値・日付・`0x…` 値・git sha）は既定では触らない
  —— rundiff は**本物の変化を絶対に隠さない**方向に強くバイアスしている。
- **正直。** 差分が信用できない場合（バイナリ出力・churn 過大・並列出力の断裂、または
  再 diff で変化の大半が正規化の残りノイズだと分かった場合）は、bounded な full 表示に
  *degrade* し、`degraded` / `degrade_reason` フィールドでそう告げる。信用できない
  差分は決して出さない。

## インストール

rundiff は Homebrew **cask**・Nix flake・ソースから配布する。ビルド済みバイナリは
[最新リリース](https://github.com/akira-toriyama/rundiff/releases)を参照。

```sh
# Homebrew (macOS/Linux)
brew install akira-toriyama/tap/rundiff

# Nix
nix run github:akira-toriyama/rundiff -- --help

# ソースから (Go 1.26+)
go install github.com/akira-toriyama/rundiff/cmd/rundiff@latest
```

## 使い方

```console
$ rundiff [flags] -- <command> [args...]
```

あるキーの初回実行が**ベースライン**になる（出力をそのまま出し、記録する）。同じキーの
2 回目以降は差分を出す。キーは `argv + cwd + git branch` なので、ブランチを切り替えたり
コマンドを変えると新しいベースラインになる。

1 行目は**常に**機械可読な JSON オブジェクト。既定モードではその後に人間/エージェント向けの
差分ボディが続き、`--json` ではレコード全体がその 1 オブジェクトになる。

| flag | 意味 |
|---|---|
| `--json` | レコード全体を 1 つの JSON オブジェクトで出す（テキストボディの代わりに `added_lines`/`removed_lines` 配列） |
| `--raw` | 正規化なしで生の行を比較する |
| `--full` | 信用できる差分があっても bounded な full 出力をボディに出す |
| `--churn <0..1>` | 変化割合がこの値に達したら full 出力に degrade する（既定 `0.5`） |
| `--tool <name\|none>` | ファイルレベル failure adapter の強制（`go-test`・`pytest`・`jest`・`vitest`・`cargo-test`・`tsc`・`eslint`）または無効化（`none`）。既定は自動検出 |

### ファイルレベルの `fixed` / `new`

ラップしたコマンドの出力が対応ツールのものと認識されると、rundiff は失敗を
**ファイルレベル**でも報告する（`go test` は package、`cargo test` はテスト名）。
`failing` は現在の run の完全な失敗集合、`fixed` / `new` はベースライン以降に
失敗しなくなった／新たに失敗し始めたもの。このチャネルは両 run の生バイトを
自前で解析するので、行差分が degrade するような大変動 run でもちょうど生き残る。

安全バイアスは行差分の逆向き：誤った `fixed` 主張は agent の探索を止めて
しまうため、**不確かなら何も言わない** —— `null` であり、`[]`（自信を持って
「なし」）とは異なる。主張が出るのは、ツール出力が完全に解析でき、ツール自身の
カウントと照合が取れ、両 run が同一ツールで、かつ過去に失敗していた identity
すべてが identity ごとの積極的証拠（pass 行 —— 失敗テストの skip や削除は
fix と数えない。クリーン run がプロジェクト全体の合格を意味する tsc/eslint
だけは全体証明も可）で説明できるときだけ。テスト名レベルの選択
（`go test -run`・pytest `-k`・jest/vitest `-t`/`--onlyChanged`・cargo の
フィルタ）の下では `fixed`/`new` を保留する：rename は同一 argv のまま
失敗中のテストを静かに選択から外せるため。

### JSON 契約

1 行目は 1 つの JSON オブジェクト（スキーマ `v:1`）。信用できる行差分が計算されなかった場合
（ベースライン、または counts を null にする degrade）、count フィールドは `null`。

| field | 型 | 意味 |
|---|---|---|
| `v` | int | スキーマ版 |
| `key` | string | キャッシュキーの 12-hex プレフィックス（どのベースラインか） |
| `exit` | int | ラップしたコマンドの終了コード（`-1` = シグナル） |
| `prev_exit` | int \| null | ベースラインの終了コード（ベースラインでは `null`） |
| `transition` | enum | `baseline` \| `still_passing` \| `still_failing` \| `fixed` \| `regressed` —— 終了コードの対から導出、常に信用可 —— または `interrupted`（実行が中断された。何も比較しない） |
| `degraded` | bool | `true` ⇒ ボディは差分でなく bounded full 出力 |
| `degrade_reason` | enum \| null | `binary` \| `too_large` \| `interleave` \| `small_output` \| `high_churn` \| `normalization_uncertain` \| `interrupted` |
| `added` / `removed` / `unchanged` | int \| null | 行数（多重集合; `unchanged` は移動した行を含む） |
| `churn` | float \| null | `(added+removed)/(added+removed+unchanged)` |
| `total_prev` / `total_cur` | int \| null | 正規化後の行数 |
| `baseline_age_s` | int \| null | ベースライン記録からの経過秒 |
| `normalized` | bool | `--raw` で `false` |
| `truncated` | bool | ボディ/配列が予算で切られた |
| `added_lines` / `removed_lines` | []string | `--json` かつ非 degrade かつ非 `--full`: 生の代表行 |
| `body` | string | `--json` のベースライン/degrade/`--full`: bounded full 出力 |
| `tool` | string \| null | run 対を完全に解析できた認識ツール（成功時に何も出力しない tsc/eslint のクリーン run は、他方 run の parser + argv hint 一致 or `--tool` で採用される）。`null` = 無主張 |
| `failing` | []string \| null | 現 run の完全な失敗 identity 集合。`null` = 無主張（「失敗なし」では**ない**）、`[]` = 自信を持って「なし」 |
| `fixed` / `new` | []string \| null | run 間の主張：失敗しなくなったと*証明された* identity ／ 以前は失敗が観測されていなかった現失敗 identity。`null` か非 null かは常に対 |

## 終了コード

rundiff は**ラップしたコマンドの終了コードを伝播**し、自分自身の失敗には慣例的な高い番号を予約する:

| code | 意味 |
|---|---|
| `0..255` | ラップしたコマンドの終了コード（伝播） |
| `125` | rundiff 自身のエラー（フラグ不正・キャッシュ/IO 失敗） |
| `126` | コマンドは在るが実行可能でない |
| `127` | コマンドが見つからない |
| `130` | 実行完了前に中断（Ctrl-C / SIGTERM） |

伝播された `125`/`126`/`127`/`130` は JSON 行で rundiff 自身のものと区別できる:
rundiff 自身のエラーは **stderr** に出て JSON 行を**出さない**。

**中断された実行（`130`）は rundiff 自身のエラーではない**: コマンドは走って途中で
切られただけなので、rundiff はレコードと**そこまでに出力された部分キャプチャ**を出す
——ラップしないコマンドを kill しても途中までのログは残るのだから、それを飲み込む
ラッパはラップしないより悪い。比較は一切行わず（`transition: "interrupted"`）、claim も
出さず、**baseline も更新しない**ので、次の完走時は最後に完走した実行と比較できる。

## pare との合成

rundiff は*何が変わったか*を出す; ベースラインや degrade では full 出力になり、大きくなりうる。
それを [`pare`](https://github.com/akira-toriyama/pare) に通して bound する —— 両者は別の軸で切る:

```sh
rundiff --full -- go test ./... | pare --profile test
```

## 設定

- `RUNDIFF_CACHE_DIR` でキャッシュディレクトリを上書きできる。無ければ
  `$XDG_CACHE_HOME/rundiff`（絶対パスのとき）または `~/.cache/rundiff` を使う。

## さらに

- [docs/algorithm.md](docs/algorithm.md) —— 正規化ルールセット・degrade 述語・JSON 契約の詳細。
- [docs/non-goals.md](docs/non-goals.md) —— rundiff が意図的にやらないこと。
