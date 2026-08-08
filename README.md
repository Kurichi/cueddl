# cueddl

Cloud Spanner のスキーマを [CUE](https://cuelang.org) で宣言的に管理するツールです。
CUE で書いた「あるべきスキーマ」と、実際のデータベースの `INFORMATION_SCHEMA` を比較し、
差分を埋める DDL を生成・適用します (GoogleSQL dialect)。

```
CUE 定義 ──(cue export)──▶ cueddl ──(diff)──▶ DDL ──(UpdateDatabaseDdl)──▶ Spanner
                                   ▲
                                   └──(INFORMATION_SCHEMA introspection)
```

## 特徴

- **宣言的**: マイグレーションファイルを書かない。スキーマの最終形だけを CUE で宣言する
- **cue cmd 統合**: `cue cmd plan` / `cue cmd apply` として CUE の tooling layer から呼び出す
- **安全**: `DROP TABLE` / `DROP COLUMN` を含む計画は明示的な許可
  (`CUEDDL_ALLOW_DESTRUCTIVE=1`) がない限り適用を拒否
- **Spanner ネイティブ**: INTERLEAVE、セカンダリインデックス (STORING / NULL_FILTERED /
  UNIQUE)、外部キー、CHECK 制約、DEFAULT 値、`allow_commit_timestamp`、
  ビュー (SQL SECURITY INVOKER / DEFINER) に対応
- **リネーム対応**: `renamedFrom` ヒントで `ALTER TABLE ... RENAME TO` を生成
  (drop + create によるデータ喪失を回避)
- データベースが存在しなければ `apply` が作成する (emulator ではインスタンスも自動作成)

## セットアップ

```sh
go get -tool github.com/Kurichi/cueddl/cmd/cueddl
go get -tool cuelang.org/go/cmd/cue
```

## スキーマを書く

```cue
package db

import "github.com/kurichi/cueddl/schema"

database: schema.#Database & {
	project:  "my-project"
	instance: "my-instance"
	name:     "my-db"

	tables: {
		Users: {
			columns: {
				UserID:    {type: "STRING(36)", notNull: true}
				Email:     {type: "STRING(MAX)", notNull: true}
				CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
			}
			primaryKey: ["UserID"]
			indexes: UsersByEmail: {
				columns: ["Email"]
				unique: true
			}
		}

		Orders: {
			columns: {
				UserID:  {type: "STRING(36)", notNull: true}
				OrderID: {type: "STRING(36)", notNull: true}
				Amount:  {type: "INT64", notNull: true}
			}
			primaryKey: ["UserID", "OrderID"]
			interleave: {parent: "Users", onDelete: "CASCADE"}
			// 作成順序を明示したい場合 (INTERLEAVE の親は自動で順序付けされる):
			// dependsOn: ["Users"]
			// テーブルをリネームしたら renamedFrom を付ける (全環境の移行後に外す):
			// renamedFrom: "OldOrders"
		}
	}

	views: {
		UserEmails: {
			definition: "SELECT u.UserID, u.Email FROM Users AS u"
			dependsOn: ["Users"]
		}
	}
}
```

`allow_commit_timestamp` を使う列は次のように宣言します:

```cue
UpdatedAt: {type: "TIMESTAMP", allowCommitTimestamp: true}
```

型・必須フィールド・相互参照 (interleave の親、FK の参照先、キー列の存在) は
CUE の unification と `cueddl` の検証で二段構えでチェックされます。

## 使う

スキーマと同じパッケージに `ddl_tool.cue` を置きます
([examples/simple/ddl_tool.cue](examples/simple/ddl_tool.cue) をコピー)。

```sh
# 差分 DDL を表示 (dry-run)
go tool cue cmd plan ./path/to/schema

# 適用 (データベースが無ければ作成)
go tool cue cmd apply ./path/to/schema

# DROP を含む変更を許可して適用
CUEDDL_ALLOW_DESTRUCTIVE=1 go tool cue cmd apply ./path/to/schema
```

`plan` の出力例:

```
Plan: 3 statement(s), 1 destructive

  ALTER TABLE `Users` ADD COLUMN `Name` STRING(MAX);

  CREATE INDEX `OrdersByStatus` ON `Orders` (`Status`) STORING (`Amount`);

! DROP TABLE `AuditLogs`;
```

認証は通常の Google Cloud のクレデンシャル (ADC) を使用します。
`SPANNER_EMULATOR_HOST` が設定されていれば emulator に接続します。

## ローカルで試す (emulator)

```sh
docker run -d --rm --name spanner -p 9010:9010 gcr.io/cloud-spanner-emulator/emulator
export SPANNER_EMULATOR_HOST=localhost:9010

go tool cue cmd apply ./examples/simple   # インスタンス・DB・スキーマを作成
go tool cue cmd plan  ./examples/simple   # -> No changes.
```

E2E テスト一式は `./scripts/e2e.sh` で実行できます (Docker が必要)。

## 差分計算の仕様

DDL は Spanner の制約を満たす順序で生成されます:

1. 不要になったビューの削除 (ビューは参照先の DDL をブロックするため最初)
2. `renamedFrom` によるテーブルリネーム
3. 不要になった外部キー・CHECK 制約・インデックスの削除
4. テーブル削除 (INTERLEAVE の子 → 親、`dependsOn` の依存元 → 依存先の順)
5. テーブル作成 (INTERLEAVE の親 → 子、`dependsOn` の依存先 → 依存元の順、CHECK はインライン)
6. 列の追加・変更 (`SET OPTIONS` / `SET DEFAULT` を含む)
7. インデックス作成 (定義が変わったものは drop + create で再作成)
8. 外部キー・CHECK 制約の追加 (全テーブル作成後なので参照順序が自由)
9. ビューの作成・置換 (`CREATE OR REPLACE`、`dependsOn` でビュー間を順序付け)
10. 列の削除 (旧定義のビューが参照している可能性があるため最後)

以下は Spanner の DDL では表現できないため、計画から除外して警告を出します:

- 主キーの変更
- INTERLEAVE 関係の変更

これらはテーブルの再作成 (データ移行) が必要です。

## 制限

- 対象は default schema (``''``) のみ。named schema・
  ROW DELETION POLICY (TTL)・生成列・change streams・PostgreSQL dialect は未対応
- 列のリネームは未対応 (Spanner の DDL に RENAME COLUMN がないため drop + add になる)
- リネーム対象のテーブルを変更前のビューが参照している場合、リネームがブロックされる
  ことがあります (ビューを一旦削除するか、2 回に分けて適用してください)
- CHECK / DEFAULT の式は軽い正規化 (空白・外側の括弧) のみで比較するため、
  意味的に同じでも書き方が大きく異なると偽の差分が出ることがあります。
  `INFORMATION_SCHEMA` が返す表記に合わせて書いてください
