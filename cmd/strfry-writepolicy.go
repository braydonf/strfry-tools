package main

import (
	"fmt"
	"os"
	"math/rand"
	"time"

	"github.com/braydonf/strfry-wot"
	"github.com/jiftechnify/strfrui"
	"github.com/jiftechnify/strfrui/sifters"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	flag "github.com/spf13/pflag"
)

var (
	k = koanf.New(".")
	cfg wot.Config

	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	// Path to one or more config files to load into koanf along with some config params.
	f.StringSlice("conf", []string{"config.yml"}, "path to one or more .yml config files")
	f.Parse(os.Args[1:])

	// Load the config files provided in the commandline.
	cFiles, _ := f.GetStringSlice("conf")
	for _, c := range cFiles {
		if err := k.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings.
	k.Unmarshal("", &cfg)

	// Set the log level.
	if cfg.LogLevel == "panic" {
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	} else if (cfg.LogLevel == "fatal") {
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	} else if (cfg.LogLevel == "error") {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	} else if (cfg.LogLevel == "warn") {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	} else if (cfg.LogLevel == "info") {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else if (cfg.LogLevel == "debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else if (cfg.LogLevel == "trace") {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		log.Warn().Str("loglevel", cfg.LogLevel).Msg("unknown log level, using 'info'")
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	authorPubKeys := make([]string, 0, len(cfg.AuthorWotUsers))

	for _, user := range cfg.AuthorWotUsers {
		authorPubKeys = append(authorPubKeys, user.PubKey)
	}

	strfrui.New(sifters.AuthorList(authorPubKeys, sifters.Allow)).Run()
}
