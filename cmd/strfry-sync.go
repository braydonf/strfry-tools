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
	"syscall"

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
	SyncCommandTimeout = 20*time.Second
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
	g.relaysMutex.Lock()
	defer g.relaysMutex.Unlock()

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

	counter strfry.ConcurrentCounter
)

func main() {
	// Setup flags, load config and set log level.
	f := SetupFlags()
	LoadConfig(f, &cfg)
	SetLogLevel(cfg.LogLevel)

	ctx := context.Background()

	monitor := SuccessMonitor{Relays: make(map[string]*RelayStatus, 0)}
	userwg := sync.WaitGroup{}

	// Sync all the users.
	for _, user := range cfg.Users {
		log.Info().Str("pubkey", user.PubKey).Msg("starting sync")

		userwg.Add(1)

		go func() {
			defer userwg.Done()

			// Make sure that we are only running the maximum number
			// of these user syncs concurrently.
			counter.Wait(strfry.MaxConcurrentSyncs)
			counter.Begin()
			defer counter.Done()

			filter := nostr.Filter{Authors: []string{user.PubKey}}
			filterBytes, err := json.Marshal(filter)
			if err != nil {
				fmt.Errorf("%s", err)
				return
			}

			filterOpt := fmt.Sprintf("--filter=%s", string(filterBytes))
			configOpt := fmt.Sprintf("--config=%s", cfg.StrFryConfig)

			for _, relay := range user.Relays {
				wg := sync.WaitGroup{}

				log.Info().Str("pubkey", user.PubKey).Str("relay", relay).Msg("with relay")

				ctx, cancel := context.WithCancel(ctx)

				cmd := exec.CommandContext(ctx, cfg.StrFryBin, configOpt, "sync", relay, filterOpt)

				// Write each sync command to a different log file
				// so that the logs are not in disorganized ordering.
				logfilePath := fmt.Sprintf("%s-pk-%s.log", cfg.StrFryLog, user.PubKey)

				logfile, err := os.Create(logfilePath)
				if err != nil {
					log.Warn().Str("user", user.PubKey).Err(err).Msg("unable to create log file")
					return
				}
				defer logfile.Close()

				outr, outw := io.Pipe()
				cmd.Stdout = os.Stdout
				cmd.Stderr = io.MultiWriter(outw, logfile)

				lastline := make(chan time.Time, 100)
				finished := make(chan bool)
				canceled := make(chan bool)

				go func() {
					var last = time.Now()

					var exit = false
					var stopped = false

					stop := func() {
						stopped = true
						syscall.Kill(cmd.Process.Pid, syscall.SIGINT)
						cancel()
					}

					for {
						select {
						case <-finished:
							exit = true
							break
						case <-canceled:
							stop()
							break
						case x := <-lastline:
							last = x
						default:
							if stopped {
								break
							}
							time.Sleep(1*time.Second)
							if time.Now().Sub(last) > SyncCommandTimeout {
								log.Warn().Str("relay", relay).Msg("negentropy timeout")
								stop()
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

						if strfry.NegentropyUnsupportedLog(lastMsg) {
							log.Warn().Str("relay", relay).Msg("negentropy unsupported")
							canceled <- true
							break
						}
					}

					if err != io.EOF && err != nil {
						log.Err(err).Str("relay", relay).Msg("readline error")
					}
				}()

				if err := cmd.Start(); err != nil {
					switch e := err.(type) {
					case *exec.Error:
						log.Err(err).Str("relay", relay).Msg("failed executing")
					case *exec.ExitError:
						log.Warn().Str("relay", relay).Int("code", e.ExitCode()).Msg("command exit")
					default:
						panic(err)
					}
				}

				wg.Add(1)
				go func() {
					defer wg.Done()

					if err := cmd.Wait(); err != nil {
						monitor.SetSuccess(relay, false)
						log.Warn().Str("relay", relay).Err(err).Msg("command exited")
					} else {
						monitor.SetSuccess(relay, true)
					}

					_ = outw.Close()
					finished <- true
				}()

				wg.Wait()

				err = monitor.WriteFile(&cfg)

				if err != nil {
					log.Err(err).Str("file", cfg.StatusFile).Msg("unable to write status file")
				} else {
					log.Info().Str("relay", relay).Str("file", cfg.StatusFile).Msg("wrote status file")
				}
			}
		}()
	}

	userwg.Wait()
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
