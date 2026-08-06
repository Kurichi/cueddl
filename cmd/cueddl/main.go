// Command cueddl reconciles a CUE-declared schema against a Cloud Spanner
// database. It is designed to be driven by the CUE tooling layer:
//
//	cue export . -e database | cueddl plan
//	cue export . -e database | cueddl apply
//
// The desired schema (a concrete schema.#Database value) arrives as JSON on
// stdin; connection coordinates are part of that value.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instance "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kurichi/cueddl/internal/diff"
	"github.com/Kurichi/cueddl/internal/introspect"
	"github.com/Kurichi/cueddl/internal/model"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cueddl:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: cueddl <plan|apply> [flags]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	allowDestructive := fs.Bool("allow-destructive", false, "permit DROP TABLE / DROP COLUMN statements")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	// The CUE tooling layer cannot easily thread CLI flags through to
	// exec.Run, so the guard can also be lifted via the environment:
	// CUEDDL_ALLOW_DESTRUCTIVE=1 cue cmd apply
	if v := os.Getenv("CUEDDL_ALLOW_DESTRUCTIVE"); v == "1" || v == "true" {
		*allowDestructive = true
	}

	desired, err := model.DecodeJSON(os.Stdin)
	if err != nil {
		return fmt.Errorf("decode desired schema: %w", err)
	}

	ctx := context.Background()
	switch cmd {
	case "plan":
		return plan(ctx, desired, os.Stdout)
	case "apply":
		return apply(ctx, desired, *allowDestructive, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q (want plan or apply)", cmd)
	}
}

func dbPath(d *model.Database) string {
	return fmt.Sprintf("projects/%s/instances/%s/databases/%s", d.Project, d.Instance, d.Name)
}

// computePlan introspects the live database (empty schema if the database
// does not exist yet) and diffs it against the desired state.
func computePlan(ctx context.Context, desired *model.Database) (*diff.Plan, bool, error) {
	admin, err := database.NewDatabaseAdminClient(ctx)
	if err != nil {
		return nil, false, err
	}
	defer admin.Close()

	exists := true
	_, err = admin.GetDatabase(ctx, &databasepb.GetDatabaseRequest{Name: dbPath(desired)})
	if status.Code(err) == codes.NotFound {
		exists = false
	} else if err != nil {
		return nil, false, fmt.Errorf("get database: %w", err)
	}

	actual := &model.Database{}
	if exists {
		client, err := spanner.NewClient(ctx, dbPath(desired))
		if err != nil {
			return nil, false, err
		}
		defer client.Close()
		actual, err = introspect.Introspect(ctx, client)
		if err != nil {
			return nil, false, err
		}
	}
	return diff.Diff(desired, actual), exists, nil
}

func printPlan(w *os.File, p *diff.Plan, dbExists bool, d *model.Database) {
	if !dbExists {
		fmt.Fprintf(w, "Database %s does not exist and will be created.\n\n", dbPath(d))
	}
	if len(p.Statements) == 0 {
		fmt.Fprintln(w, "No changes. The database schema matches the configuration.")
	} else {
		destructive := 0
		for _, s := range p.Statements {
			if s.Destructive {
				destructive++
			}
		}
		fmt.Fprintf(w, "Plan: %d statement(s)", len(p.Statements))
		if destructive > 0 {
			fmt.Fprintf(w, ", %d destructive", destructive)
		}
		fmt.Fprint(w, "\n\n")
		for _, s := range p.Statements {
			marker := "  "
			if s.Destructive {
				marker = "! "
			}
			fmt.Fprintf(w, "%s%s;\n\n", marker, s.SQL)
		}
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintln(w, "Warnings:")
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  - %s\n", warn)
		}
	}
}

func plan(ctx context.Context, desired *model.Database, w *os.File) error {
	p, exists, err := computePlan(ctx, desired)
	if err != nil {
		return err
	}
	printPlan(w, p, exists, desired)
	return nil
}

func apply(ctx context.Context, desired *model.Database, allowDestructive bool, w *os.File) error {
	p, exists, err := computePlan(ctx, desired)
	if err != nil {
		return err
	}
	printPlan(w, p, exists, desired)

	if p.HasDestructive() && !allowDestructive {
		return fmt.Errorf("plan contains destructive statements; rerun with --allow-destructive to apply them")
	}

	admin, err := database.NewDatabaseAdminClient(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()

	if !exists {
		if os.Getenv("SPANNER_EMULATOR_HOST") != "" {
			if err := ensureEmulatorInstance(ctx, desired); err != nil {
				return err
			}
		}
		op, err := admin.CreateDatabase(ctx, &databasepb.CreateDatabaseRequest{
			Parent:          fmt.Sprintf("projects/%s/instances/%s", desired.Project, desired.Instance),
			CreateStatement: fmt.Sprintf("CREATE DATABASE `%s`", desired.Name),
		})
		if err != nil {
			return fmt.Errorf("create database: %w", err)
		}
		if _, err := op.Wait(ctx); err != nil {
			return fmt.Errorf("create database: %w", err)
		}
		fmt.Fprintf(w, "Created database %s.\n", dbPath(desired))
	}

	if len(p.Statements) == 0 {
		return nil
	}

	stmts := make([]string, len(p.Statements))
	for i, s := range p.Statements {
		stmts[i] = s.SQL
	}
	op, err := admin.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:   dbPath(desired),
		Statements: stmts,
	})
	if err != nil {
		return fmt.Errorf("update ddl: %w", err)
	}
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("update ddl: %w", err)
	}
	fmt.Fprintf(w, "Applied %d statement(s).\n", len(stmts))
	return nil
}

// ensureEmulatorInstance creates the target instance on the emulator so a
// fresh `cue cmd apply` works against a just-started container.
func ensureEmulatorInstance(ctx context.Context, d *model.Database) error {
	admin, err := instance.NewInstanceAdminClient(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()

	name := fmt.Sprintf("projects/%s/instances/%s", d.Project, d.Instance)
	_, err = admin.GetInstance(ctx, &instancepb.GetInstanceRequest{Name: name})
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.NotFound {
		return fmt.Errorf("get instance: %w", err)
	}
	op, err := admin.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
		Parent:     "projects/" + d.Project,
		InstanceId: d.Instance,
		Instance: &instancepb.Instance{
			Config:      fmt.Sprintf("projects/%s/instanceConfigs/emulator-config", d.Project),
			DisplayName: d.Instance,
			NodeCount:   1,
		},
	})
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	return nil
}
