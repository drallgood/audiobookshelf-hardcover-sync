// Package main provides the standalone Hardcover edition creation CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/urfave/cli/v2"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// defaultConfigPath is optional; a file named with --config must exist.
const defaultConfigPath = "config.yaml"

func main() {
	if !isHelpOrVersion(os.Args[1:]) {
		configPath, explicitConfig := findConfigPath(os.Args[1:])
		cfg, configErr := loadEditionConfig(configPath, explicitConfig)
		if configErr != nil {
			logger.Setup(logger.Config{
				Level:      "info",
				Format:     logger.FormatConsole,
				Output:     os.Stderr,
				TimeFormat: time.RFC3339,
			})
		} else {
			logger.Setup(logger.Config{
				Level:      cfg.Logging.Level,
				Format:     logger.ParseLogFormat(cfg.Logging.Format),
				Output:     os.Stderr,
				TimeFormat: time.RFC3339,
			})
		}
	}

	app := newApp()
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exitCoder cli.ExitCoder
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}

func isHelpOrVersion(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "--version" || arg == "-v" || arg == "help" {
			return true
		}
	}
	return false
}

func newApp() *cli.App {
	return &cli.App{
		Name:      "edition",
		Usage:     "Create and manage editions in Hardcover",
		Version:   fmt.Sprintf("%s (%s) %s", version, commit, date),
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Load configuration from `FILE`",
				Value:   defaultConfigPath,
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Validate and preview without making Hardcover changes",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Import an audiobook or create an ebook edition",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "input",
						Aliases:  []string{"i"},
						Usage:    "Input JSON file with edition data",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "abs-item-id",
						Usage: "Audiobookshelf item ID to verify and associate in sync state",
					},
					&cli.StringFlag{
						Name:  "state-file",
						Usage: "Sync state file for a confirmed Audiobookshelf association (default: sync.state_file)",
					},
				},
				Action: createEdition,
			},
			{
				Name:  "prepopulate",
				Usage: "Generate a prepopulated JSON template for a book",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:     "book-id",
						Usage:    "Hardcover book ID to prepopulate from",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "Output JSON file",
						Value:   "edition-template.json",
					},
				},
				Action: prepopulateEdition,
			},
		},
	}
}

func createEdition(c *cli.Context) error {
	cfg, err := loadEditionConfig(c.String("config"), c.IsSet("config"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	dryRun := cfg.Sync.DryRun
	if c.IsSet("dry-run") {
		dryRun = c.Bool("dry-run")
	}
	stateFile := c.String("state-file")
	if stateFile == "" {
		stateFile = cfg.Sync.StateFile
	}
	services, err := newCreateServices(cfg, logger.Get(), dryRun)
	if err != nil {
		return err
	}
	result, err := runCreate(context.Background(), createOptions{
		InputPath:       c.String("input"),
		ABSItemID:       c.String("abs-item-id"),
		StateFile:       stateFile,
		PreferredRegion: cfg.Audiobookshelf.AudnexusRegion,
		DryRun:          dryRun,
	}, services)
	if err != nil {
		return err
	}
	return writeJSON(c.App.Writer, result)
}

func prepopulateEdition(c *cli.Context) error {
	cfg, err := loadEditionConfig(c.String("config"), c.IsSet("config"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	log := logger.Get()
	clientConfig := hardcoverClientConfig(cfg.Hardcover.BaseURL)
	hc := hardcover.NewClientWithConfig(clientConfig, cfg.Hardcover.Token, log)
	dryRun := cfg.Sync.DryRun
	if c.IsSet("dry-run") {
		dryRun = c.Bool("dry-run")
	}
	hc.SetDryRun(dryRun)
	creator := edition.NewCreator(hc, log, dryRun, cfg.Audiobookshelf.Token)
	if err := creator.SetAudiobookshelfNetworkTrust(cfg.Audiobookshelf.NetworkTrust); err != nil {
		return fmt.Errorf("invalid Audiobookshelf network trust: %w", err)
	}
	if err := creator.SetAudiobookshelfBaseURL(cfg.Audiobookshelf.URL); err != nil {
		return fmt.Errorf("invalid Audiobookshelf URL: %w", err)
	}

	prepopulated, err := creator.PrepopulateFromBook(context.Background(), c.Int("book-id"))
	if err != nil {
		return fmt.Errorf("failed to prepopulate data: %w", err)
	}
	outputFile := c.String("output")
	if err := writeJSONFile(outputFile, prepopulated); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}
	_, err = fmt.Fprintf(c.App.Writer, "Prepopulated data written to %s\n", outputFile)
	return err
}

// loadEditionConfig applies defaults, the YAML file, and environment overrides
// without the sync service's required fields or configuration dump. A missing
// default config.yaml leaves environment-only configuration; a file named with
// --config must exist.
func loadEditionConfig(path string, explicit bool) (*config.Config, error) {
	if !explicit {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			path = ""
		}
	}
	return config.LoadForTool(path)
}
