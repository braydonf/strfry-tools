package main

import (
	"fmt"
	"os"
	"time"
	"strings"
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

type RelayStatus struct {
	Url string `json:"url"`
	Msg string `json:"msg",omitempty`
	Success bool `json:"success"`
	Time *time.Time `json:"time,omitempty"`
}

type SuccessMonitor struct {
	Relays map[string]*RelayStatus `json:"relays"`
	relaysMutex sync.RWMutex
}

func (g *SuccessMonitor) SetSuccess(relay string, status bool) {
	g.relaysMutex.Lock()

	if g.Relays[relay] == nil {
		g.Relays[relay] = &RelayStatus{Url: relay}
	}

	g.Relays[relay].Success = status

	g.relaysMutex.Unlock()
}

func (g *SuccessMonitor) SetTimeAndMsg(relay string, now time.Time, lastMsg string) {
	g.relaysMutex.Lock()

	if g.Relays[relay] == nil {
		g.Relays[relay] = &RelayStatus{Url: relay}
	}

	if len(lastMsg) > 0 {
		g.Relays[relay].Msg = strings.Clone(lastMsg)
	}

	g.Relays[relay].Time = &now

	g.relaysMutex.Unlock()
}

func (g *SuccessMonitor) WriteFile(cfg *strfry.SyncConfig) error {
	bytes, err := json.MarshalIndent(g, "", "  ")

	if err != nil {
		return fmt.Errorf("marshal error: %s", err)
	} else {
		if err := os.WriteFile(cfg.StatusFile, bytes, 0644); err != nil {
			return fmt.Errorf("write error: %s", err)
		}
	}

	return nil
}

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

	monitor := SuccessMonitor{Relays: make(map[string]*RelayStatus, 0)}

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
			cmd.Stdout = os.Stdout
			cmd.Stderr = io.MultiWriter(outw, os.Stdout)

			lastline := make(chan time.Time, 100)
			finished := make(chan bool)

			go func() {
				var last = time.Now()
				exit := false

				for {
					select {
					case <-finished:
						exit = true
						break
					case x := <-lastline:
						last = x
					default:
						time.Sleep(1*time.Second)

						if time.Now().Sub(last) > SyncCommandTimeout {
							cancel()
							break
						}
					}

					if exit {
						break
					}
				}
			}()

			go func() {
				reader := bufio.NewReader(outr)

				var err error
				var lastMsg []byte

				for ; err == nil; {
					lastMsg, _, err = reader.ReadLine()

					monitor.SetTimeAndMsg(relay, time.Now(), string(lastMsg))

					lastline <- time.Now()
				}

				if err != io.EOF && err != nil {
					fmt.Println("readline error:", err)
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

				if err := cmd.Wait(); err != nil {
					monitor.SetSuccess(relay, false)
					fmt.Println("command exited:", err)
				} else {
					monitor.SetSuccess(relay, true)
				}

				_ = outw.Close()
				finished <- true
			}()

			wg.Wait()

			err := monitor.WriteFile(&cfg)

			if err != nil {
				log.Err(err).Str("file", cfg.StatusFile).Msg("unable to write status file")
			} else {
				log.Info().Str("relay", relay).Str("file", cfg.StatusFile).Msg("wrote status file")
			}
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
