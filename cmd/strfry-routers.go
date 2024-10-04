package main

import (
	"fmt"
	"os"
	"time"
	"os/exec"
	"context"
	"sync"
	"path/filepath"

	"github.com/braydonf/strfry-tools"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	flag "github.com/spf13/pflag"
)

var (
	knf = koanf.New(".")
	cfg strfry.RoutersConfig
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
)

type Router struct {
	Config string
}

func (g *Router) Run(ctx context.Context) {
	wg := sync.WaitGroup{}

	configOpt := fmt.Sprintf("--config=%s", cfg.StrFryConfig)

	log.Info().Str("router", g.Config).Msg("starting router")

	ctx, _ = context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, cfg.StrFryBin, configOpt, "router", g.Config)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		switch e := err.(type) {
		case *exec.Error:
			log.Err(err).Str("config", g.Config).Msg("failed executing")
		case *exec.ExitError:
			log.Warn().Str("config", g.Config).Int("code", e.ExitCode()).Msg("command exit")
		default:
			panic(err)
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := cmd.Wait(); err != nil {
			log.Warn().Str("config", g.Config).Err(err).Msg("command exited")
		}
	}()

	wg.Wait()
}

func main() {
	log.Info().Msg("routers starting")

	f := SetupFlags()
	LoadConfig(f, &cfg)
	SetLogLevel(cfg.LogLevel)

	ctx := context.Background()

	userwg := sync.WaitGroup{}
	exited := false

	filePattern := fmt.Sprintf("%s-pk-*.config", cfg.ConfigBase)
	fmt.Println(filePattern)
	files, err := filepath.Glob(filePattern)
	if err != nil {
		log.Err(err).Msg("error listing router configs")
	}

	if len(files) <= 0 {
		log.Warn().Str("config-base", cfg.ConfigBase).Msg("no config files found")
	}

	for _, file := range files {
		userwg.Add(1)

		go func() {
			defer userwg.Done()

			for {
				log.Info().Str("config", file).Msg("starting router")
				router := &Router{Config: file}
				router.Run(ctx)
				log.Info().Str("config", file).Msg("router exited")
				if exited {
					break
				}

				st := 60 * time.Second
				log.Info().Str("config", file).Str("sleep", st.String()).Msg("will restart")
				time.Sleep(st)
			}
		}()
	}

	userwg.Wait()

	log.Info().Msg("routers ended")
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

	f.StringSlice("conf", []string{"routers-config.json"}, "path to routers config file")

	f.Parse(os.Args[1:])

	return f
}

func LoadConfig(f *flag.FlagSet, cfg *strfry.RoutersConfig) {
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
