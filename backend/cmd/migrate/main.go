package main

import (
	"fmt"
	"os"

	"github.com/mazhit-a/mini-kedo/internal/config"
	"github.com/mazhit-a/mini-kedo/internal/repository/postgres"
	"github.com/mazhit-a/mini-kedo/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "down") {
		return fmt.Errorf("usage: migrate up|down")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	switch args[0] {
	case "down":
		err = postgres.MigrateDown(cfg.DatabaseURL, migrations.FS)
	default:
		err = postgres.MigrateUp(cfg.DatabaseURL, migrations.FS)
	}
	if err != nil {
		return err
	}
	fmt.Println("migrate", args[0], "ok")
	return nil
}
