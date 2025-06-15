package migrate

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"

	"github.com/pressly/goose/v3"
)

//go:embed migrations
var baseFS embed.FS

var migrationLock sync.Mutex

// registerFuncs is a list of functions that register migrations.
// Each migration file should have an init function that appends their register function to this list.
// This is different from the goose registration which is public for all packages.
var registerFuncs = []func(){}

// RegisterFuncs returns the list of functions for registering goose migrations.
func RegisterFuncs() []func() {
	return registerFuncs
}

// RunGoose runs the goose command with the provided arguments.
// args should be the command and the arguments to pass to goose.
// eg RunGoose(ctx, []string{"up", "-v"}, db).
func RunGoose(ctx context.Context, gooseArgs []string, db *sql.DB) error {
	migrationLock.Lock()
	defer migrationLock.Unlock()
	if len(gooseArgs) == 0 {
		return fmt.Errorf("command not provided")
	}
	cmd := gooseArgs[0]
	var args []string
	if len(gooseArgs) > 1 {
		args = gooseArgs[1:]
	}
	setMigrations(baseFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}
	err := goose.RunContext(ctx, cmd, db, ".", args...)
	if err != nil {
		return fmt.Errorf("failed to run goose command: %w", err)
	}
	return nil
}

// setMigrations sets the migrations for the goose tool.
// this will reset the global migrations and FS to avoid any unwanted migrations registers.
func setMigrations(baseFS embed.FS) {
	goose.SetBaseFS(baseFS)
	goose.ResetGlobalMigrations()
}
