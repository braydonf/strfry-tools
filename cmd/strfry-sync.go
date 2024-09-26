package main

import (
	"fmt"
	"os"
	"time"
	"os/exec"
	"encoding/json"
	"context"
	"sync"
	"io"
	"bufio"

	"github.com/braydonf/strfry-tools"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/nbd-wtf/go-nostr"
	"github.com/rs/zerolog"
	flag "github.com/spf13/pflag"
)

const (
	SyncCommandTimeout = 10 * time.Second
)

var (
	knf = koanf.New(".")
	cfg strfry.SyncConfig
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})
)

func main() {
	// Setup flags, load config and set log level.
	f := SetupFlags()
	LoadConfig(f, &cfg)
	SetLogLevel(cfg.LogLevel)

	ctx := context.Background()

	// Sync all the users.
	for _, user := range cfg.Users {
		log.Info().Str("pubkey", user.PubKey).Msg("starting sync")

		filter := nostr.Filter{Authors: []string{user.PubKey}}
		filterBytes, err := json.Marshal(filter)
		if err != nil {
			fmt.Errorf("%s", err)
			break
		}

		filterOpt := fmt.Sprintf("--filter=%s", string(filterBytes))
		configOpt := fmt.Sprintf("--config=%s", cfg.StrFryConfig)

		for _, relay := range user.Relays {
			wg := sync.WaitGroup{}

			log.Info().Str("pubkey", user.PubKey).Str("relay", relay).Msg("with relay")

			ctx, cancel := context.WithCancel(ctx)

			cmd := exec.CommandContext(ctx, cfg.StrFryBin, configOpt, "sync", relay, filterOpt)

			outr, outw := io.Pipe()
			cmd.Stdout = io.MultiWriter(outw, os.Stdout)
			cmd.Stderr = os.Stderr

			lines := make(chan string)
			lastLine := time.Now()
			finished := false

			go func() {
				for {
					if finished {
						break
					}
					if time.Now().Sub(lastLine) > SyncCommandTimeout {
						cancel()
						break
					}
					time.Sleep(1*time.Second)
				}
			}()

			go func() {
				for line := range lines {
					lastLine = time.Now()
					fmt.Println(line)
				}
			}()

			go func() {
				defer close(lines)

				scanner := bufio.NewScanner(outr)

				for scanner.Scan() {
					lines <- scanner.Text()
				}

				if err := scanner.Err(); err != nil {
					fmt.Println("scanner error:", err)
				}
			}()

			if err := cmd.Start(); err != nil {
				switch e := err.(type) {
				case *exec.Error:
					fmt.Println("failed executing:", err)
				case *exec.ExitError:
					fmt.Println("command exit code:", e.ExitCode())
				default:
					panic(err)
				}
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				err := cmd.Wait()
				if err != nil {
					fmt.Println("command exited:", err)
				}
				_ = outw.Close()
				finished = true
			}()

			wg.Wait()
		}
	}
}

func SetLogLevel(lvl string) {
	if lvl == "panic" {
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	} else if (lvl == "fatal") {
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	} else if (lvl == "error") {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	} else if (lvl == "warn") {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	} else if (lvl == "info") {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else if (lvl == "debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else if (lvl == "trace") {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func SetupFlags() *flag.FlagSet {
	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	f.StringSlice(
		"conf",
		[]string{"router.yml"},
		"path to one or more .yml router config files")

	f.Parse(os.Args[1:])

	return f
}

func LoadConfig(f *flag.FlagSet, cfg *strfry.SyncConfig) {
	// Load the config files provided in the commandline.
	filepaths, _ := f.GetStringSlice("conf")
	for _, c := range filepaths {
		if err := knf.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := knf.Load(posflag.Provider(f, ".", knf), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings and set the log level.
	knf.Unmarshal("", &cfg)
}
