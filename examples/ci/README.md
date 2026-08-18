# CI ワークフローの例

`database.cue` 自体はこの例の本題ではなく、隣の `github-workflows/` にある
3つのワークフローが本題。cueddl を使ってスキーマ変更を CI に組み込む場合の、
3段階の運用フローの参考実装。

コピー先はリポジトリの `.github/workflows/`（このディレクトリ名が
`github-workflows/` なのは、cueddl 自身のリポジトリでこれらを実行させない
ため）。`schema-path` や認証まわりは自分のプロジェクトに合わせて書き換える。

## 3段階フロー

```mermaid
flowchart TD
    A[PRを作成/更新] --> B["Stage 1: schema-preview.yml\ncueddl plan --against base-sha\n(実DB接続なし・認証情報なし)"]
    A --> C["Stage 2: schema-plan.yml\ncue export | cueddl plan\n(実DBに接続・認証必要)"]
    C --> D[コメントとして投稿される\n以降の apply が検証する基準]
    D --> E[レビュー・承認]
    E --> F["`cueddl apply` とコメント"]
    F --> G["Stage 3: schema-apply.yml\napply直前に再度 plan を計算し\nStage2のコメントと完全一致するか検証"]
    G -->|一致| H[cueddl apply を実行]
    G -->|不一致| I["失敗\n(他のPRが先に適用した可能性)"]
```

### なぜ3段階に分けるか

- **Stage 1 (`schema-preview.yml`)**: 実DBに一切接続しないので、認証情報が
  要らない。PRを開いた瞬間から「このPRでどんなDDLになるか」がわかる。
  cueddl 本体の `plan --against` 機能をそのまま使っているだけ。
- **Stage 2 (`schema-plan.yml`)**: ここで初めて実DBに接続し、「今のDBに
  対して本当は何が起きるか」を計算する。これが `apply` の根拠になる、
  唯一の正しいplan。PRコメントとして残す。
- **Stage 3 (`schema-apply.yml`)**: `cueddl apply` コメントで起動。
  **apply する直前にもう一度 live DB に対して plan を計算し直し、
  Stage 2 のコメントと一致するかを確認する。** 一致しなければ拒否する。

Stage 3 がある理由: 承認された時点のplanと、実際にapplyする時点の実DBの
状態が一致している保証がないと、**別のPRが先にマージ・適用されていた場合に
それを見逃して上書き適用してしまう**。Terraform の `plan -out=file` →
`apply file` が「plan後にstateが変わっていたら拒否する」のと同じ理屈を、
「保存したplanのテキストと、apply直前に取り直したplanのテキストを比較する」
という形でcueddl本体に手を入れずに実現している。

### 制約・今後の検討

- Stage 3 の「一致確認」は PR コメントのテキスト比較というやや素朴な実装。
  コメント形式を変えると壊れるので、チームの運用に合わせて調整すること。
- より頑健にするなら、cueddl 自体に `plan -out=<file>` /
  `apply -in=<file>`（保存したplanでしかapplyできず、apply前に自動でdrift
  チェックする）を持たせる方向性もある。今回は既存の `plan --against` だけで
  組める設計をあえて選んでいる。
- `schema-plan.yml` / `schema-apply.yml` はいずれも実際の GitHub Actions
  runner 上では未検証（YAML構文と actionlint によるチェックのみ）。自分の
  プロジェクトに導入する際は、まず読み取り専用の `schema-plan.yml` から
  小さく試すことを推奨する。
