package lambroll

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/fatih/color"
	"github.com/fujiwara/logutils"
)

type Option struct {
	Function string `help:"Function file path" env:"LAMBROLL_FUNCTION"`
	LogLevel string `help:"log level (trace, debug, info, warn, error)" default:"info" enum:"trace,debug,info,warn,error" env:"LAMBROLL_LOGLEVEL"`
	Color    bool   `help:"enable colored output" default:"true" env:"LAMBROLL_COLOR"`

	Region          *string           `help:"AWS region" env:"AWS_REGION"`
	Profile         *string           `help:"AWS credential profile name" env:"AWS_PROFILE"`
	TFState         *string           `name:"tfstate" help:"URL to terraform.tfstate" env:"LAMBROLL_TFSTATE"`
	PrefixedTFState map[string]string `name:"prefixed-tfstate" help:"key value pair of the prefix for template function name and URL to terraform.tfstate" env:"LAMBROLL_PREFIXED_TFSTATE"`
	Endpoint        *string           `help:"AWS API Lambda Endpoint" env:"AWS_LAMBDA_ENDPOINT"`
	Envfile         []string          `help:"environment files" env:"LAMBROLL_ENVFILE"`
	ExtStr          map[string]string `help:"external string values for Jsonnet" env:"LAMBROLL_EXTSTR"`
	ExtCode         map[string]string `help:"external code values for Jsonnet" env:"LAMBROLL_EXTCODE"`
}

type CLIOptions struct {
	Option

	Deploy   *DeployOption   `cmd:"deploy" help:"deploy or create function"`
	Init     *InitOption     `cmd:"init" help:"init function.json"`
	List     *ListOption     `cmd:"list" help:"list functions"`
	Rollback *RollbackOption `cmd:"rollback" help:"rollback function"`
	Invoke   *InvokeOption   `cmd:"invoke" help:"invoke function"`
	Archive  *ArchiveOption  `cmd:"archive" help:"archive function"`
	Logs     *LogsOption     `cmd:"logs" help:"show logs of function"`
	Diff     *DiffOption     `cmd:"diff" help:"show diff of function"`
	Render   *RenderOption   `cmd:"render" help:"render function.json"`
	Status   *StatusOption   `cmd:"status" help:"show status of function"`
	Delete   *DeleteOption   `cmd:"delete" help:"delete function"`
	Versions *VersionsOption `cmd:"versions" help:"show versions of function"`

	Version struct{} `cmd:"version" help:"show version"`
}

type CLIParseFunc func([]string) (string, *CLIOptions, func(), error)

func ParseCLI(args []string) (string, *CLIOptions, func(), error) {
	// compatible with v1
	if len(args) == 0 || len(args) > 0 && args[0] == "help" {
		args = []string{"--help"}
	}

	var opts CLIOptions
	parser, err := kong.New(&opts, kong.Vars{"version": Version})
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to new kong: %w", err)
	}
	c, err := parser.Parse(args)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to parse args: %w", err)
	}
	sub := strings.Fields(c.Command())[0]
	return sub, &opts, func() { c.PrintUsage(true) }, nil
}

func CLI(ctx context.Context, parse CLIParseFunc) (int, error) {
	sub, opts, usage, err := parse(os.Args[1:])
	if err != nil {
		return 1, err
	}

	color.NoColor = !opts.Color
	filter := &logutils.LevelFilter{
		Levels: []logutils.LogLevel{"trace", "debug", "info", "warn", "error"},
		ModifierFuncs: []logutils.ModifierFunc{
			logutils.Color(color.FgHiWhite), // trace
			logutils.Color(color.FgHiBlack), // debug
			nil,                             // info
			logutils.Color(color.FgYellow),  // warn
			logutils.Color(color.FgRed),     // error
		},
		MinLevel: logutils.LogLevel(opts.LogLevel),
		Writer:   os.Stderr,
	}
	log.SetOutput(filter)

	if err := dispatchCLI(ctx, sub, usage, opts); err != nil {
		return 1, err
	}
	return 0, nil
}

func dispatchCLI(ctx context.Context, sub string, usage func(), opts *CLIOptions) error {
	switch sub {
	case "version", "":
		fmt.Println("lambroll", Version)
		return nil
	}

	app, err := New(ctx, &opts.Option)
	if err != nil {
		return err
	}
	if opts.Function != "" {
		log.Printf("[info] lambroll %s with %s", Version, opts.Function)
	} else {
		log.Printf("[info] lambroll %s", Version)
	}
	switch sub {
	case "init":
		return app.Init(ctx, opts.Init)
	case "list":
		return app.List(ctx, opts.List)
	case "deploy":
		return app.Deploy(ctx, opts.Deploy)
	case "invoke":
		return app.Invoke(ctx, opts.Invoke)
	case "logs":
		return app.Logs(ctx, opts.Logs)
	case "versions":
		return app.Versions(ctx, opts.Versions)
	case "archive":
		return app.Archive(ctx, opts.Archive)
	case "rollback":
		return app.Rollback(ctx, opts.Rollback)
	case "render":
		return app.Render(ctx, opts.Render)
	case "diff":
		return app.Diff(ctx, opts.Diff)
	case "delete":
		return app.Delete(ctx, opts.Delete)
	case "status":
		return app.Status(ctx, opts.Status)
	default:
		usage()
	}
	return nil
}
