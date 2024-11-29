package lambroll

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/fujiwara/ssm-lookup/ssm"
	"github.com/fujiwara/tfstate-lookup/tfstate"
	"github.com/google/go-jsonnet"
	"github.com/hashicorp/go-envparse"
	"github.com/kayac/go-config"
	"github.com/shogo82148/go-retry"
)

var Version string

const (
	versionLatest    = "$LATEST"
	packageTypeImage = "Image"
)

var retryPolicy = retry.Policy{
	MinDelay: time.Second,
	MaxDelay: 10 * time.Second,
	MaxCount: 30,
}

// Function represents configuration of Lambda function
// type Function = lambda.CreateFunctionInput
type Function lambda.CreateFunctionInput

// Tags represents tags of function
type Tags map[string]string

func (app *App) functionArn(ctx context.Context, name string) string {
	return fmt.Sprintf(
		"arn:aws:lambda:%s:%s:function:%s",
		app.awsConfig.Region,
		app.AWSAccountID(ctx),
		name,
	)
}

var (
	// DefaultLogLevel is default log level
	DefaultLogLevel = "info"

	// IgnoreFilename defines file name includes ignore patterns at creating zip archive.
	IgnoreFilename = ".lambdaignore"

	// DefaultFunctionFilename defines file name for function definition.
	DefaultFunctionFilenames = []string{
		"function.json",
		"function.jsonnet",
	}

	// DefaultFunctionURLFilenames defines file name for function URL definition.
	DefaultFunctionURLFilenames = []string{
		"function_url.json",
		"function_url.jsonnet",
	}

	// DefaultOptionFilenames defines file name for option definition.
	DefaultOptionFilenames = []string{
		"lambroll.json",
		"lambroll.jsonnet",
	}

	// FunctionZipFilename defines file name for zip archive downloaded at init.
	FunctionZipFilename = "function.zip"

	// DefaultExcludes is a preset excludes file list
	DefaultExcludes = []string{
		IgnoreFilename,
		DefaultFunctionFilenames[0],
		DefaultFunctionFilenames[1],
		DefaultFunctionURLFilenames[0],
		DefaultFunctionURLFilenames[1],
		DefaultOptionFilenames[0],
		DefaultOptionFilenames[1],
		FunctionZipFilename,
		".git/*",
		".terraform/*",
		"terraform.tfstate",
	}

	// CurrentAliasName is alias name for current deployed function
	CurrentAliasName = "current"
)

// App represents lambroll application
type App struct {
	callerIdentity *CallerIdentity
	profile        string
	loader         *config.Loader

	awsConfig aws.Config
	lambda    *lambda.Client

	extStr      map[string]string
	extCode     map[string]string
	nativeFuncs []*jsonnet.NativeFunction

	functionFilePath string
}

func newAwsConfig(ctx context.Context, opt *Option) (aws.Config, error) {
	var region string
	if opt.Region != nil && *opt.Region != "" {
		region = aws.ToString(opt.Region)
	}
	optFuncs := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if opt.Endpoint != nil && *opt.Endpoint != "" {
		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if service == lambda.ServiceID || service == sts.ServiceID || service == s3.ServiceID {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           *opt.Endpoint,
					SigningRegion: region,
				}, nil
			}
			// returning EndpointNotFoundError will allow the service to fallback to it's default resolution
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		})
		optFuncs = append(optFuncs, awsconfig.WithEndpointResolverWithOptions(customResolver))
	}
	if opt.Profile != nil && *opt.Profile != "" {
		optFuncs = append(optFuncs, awsconfig.WithSharedConfigProfile(*opt.Profile))
	}
	return awsconfig.LoadDefaultConfig(ctx, optFuncs...)
}

// New creates an application
func New(ctx context.Context, opt *Option) (*App, error) {
	for _, envfile := range opt.Envfile {
		if err := exportEnvFile(envfile); err != nil {
			return nil, err
		}
	}

	v2cfg, err := newAwsConfig(ctx, opt)
	if err != nil {
		return nil, err
	}

	var profile string
	if opt.Profile != nil && *opt.Profile != "" {
		profile = *opt.Profile
	}

	loader := config.New()
	nativeFuncs := DefaultJsonnetNativeFuncs()

	// load ssm functions
	if ssmFuncs, err := ssm.FuncMap(ctx, v2cfg); err != nil {
		return nil, err
	} else {
		loader.Funcs(ssmFuncs)
	}
	if ssmNativeFuncs, err := ssm.JsonnetNativeFuncs(ctx, v2cfg); err != nil {
		return nil, err
	} else {
		nativeFuncs = append(nativeFuncs, ssmNativeFuncs...)
	}

	// load tfstate functions
	if opt.TFState != nil && *opt.TFState != "" {
		lookup, err := tfstate.ReadURL(ctx, *opt.TFState)
		if err != nil {
			return nil, err
		}
		loader.Funcs(lookup.FuncMap(ctx))
		nativeFuncs = append(nativeFuncs, lookup.JsonnetNativeFuncs(ctx)...)
	}
	if len(opt.PrefixedTFState) > 0 {
		prefixedFuncs := make(template.FuncMap)
		for prefix, path := range opt.PrefixedTFState {
			if prefix == "" {
				return nil, fmt.Errorf("--prefixed-tfstate option cannot have empty key")
			}
			loader, err := tfstate.ReadURL(ctx, path)
			if err != nil {
				return nil, err
			}
			for name, f := range loader.FuncMap(ctx) {
				prefixedFuncs[prefix+name] = f
			}
			nativeFuncs = append(nativeFuncs, loader.JsonnetNativeFuncsWithPrefix(ctx, prefix)...)
		}
		loader.Funcs(prefixedFuncs)
	}

	callerIdentity := newCallerIdentity(v2cfg)
	nativeFuncs = append(nativeFuncs, callerIdentity.JsonnetNativeFuncs(ctx)...)
	loader.Funcs(callerIdentity.FuncMap(ctx))

	app := &App{
		callerIdentity:   callerIdentity,
		profile:          profile,
		loader:           loader,
		awsConfig:        v2cfg,
		lambda:           lambda.NewFromConfig(v2cfg),
		functionFilePath: opt.Function,
		nativeFuncs:      nativeFuncs,
		extStr:           opt.ExtStr,
		extCode:          opt.ExtCode,
	}
	return app, nil
}

// AWSAccountID returns AWS account ID in current session
func (app *App) AWSAccountID(ctx context.Context) string {
	return app.callerIdentity.Account(ctx)
}

func loadDefinitionFile[T any](app *App, path string, defaults []string) (*T, error) {
	if path == "" {
		p, err := findDefinitionFile("", defaults)
		if err != nil {
			return nil, err
		}
		path = p
	}
	var instance T
	typeName := reflect.TypeOf(instance).Name()
	log.Printf("[info] loading %s from %s", typeName, path)

	var (
		src []byte
		err error
	)
	switch filepath.Ext(path) {
	case ".jsonnet":
		vm := jsonnet.MakeVM()
		if app != nil {
			for _, f := range app.nativeFuncs {
				vm.NativeFunction(f)
			}
			for k, v := range app.extStr {
				vm.ExtVar(k, v)
			}
			for k, v := range app.extCode {
				vm.ExtCode(k, v)
			}
		}
		jsonStr, err := vm.EvaluateFile(path)
		if err != nil {
			return nil, err
		}
		if app != nil {
			src, err = app.loader.ReadWithEnvBytes([]byte(jsonStr))
			if err != nil {
				return nil, err
			}
		} else {
			// no app, just return jsonnet result
			src = []byte(jsonStr)
		}
	default:
		if app != nil {
			src, err = app.loader.ReadWithEnv(path)
			if err != nil {
				return nil, err
			}
		} else {
			src, err = os.ReadFile(path)
			if err != nil {
				return nil, err
			}
		}
	}
	var v T
	if err := unmarshalJSON(src, &v, path); err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", path, err)
	}
	return &v, nil
}

func (app *App) loadFunction(path string) (*Function, error) {
	return loadDefinitionFile[Function](app, path, DefaultFunctionFilenames)
}

func newFunctionFrom(c *types.FunctionConfiguration, code *types.FunctionCodeLocation, tags Tags) *Function {
	if c == nil {
		return nil
	}
	fn := &Function{
		Architectures:     c.Architectures,
		Description:       c.Description,
		EphemeralStorage:  c.EphemeralStorage,
		FunctionName:      c.FunctionName,
		Handler:           c.Handler,
		LoggingConfig:     c.LoggingConfig,
		MemorySize:        c.MemorySize,
		Role:              c.Role,
		Runtime:           c.Runtime,
		Timeout:           c.Timeout,
		DeadLetterConfig:  c.DeadLetterConfig,
		FileSystemConfigs: c.FileSystemConfigs,
		KMSKeyArn:         c.KMSKeyArn,
		SnapStart:         newSnapStart(c.SnapStart),
	}

	if e := c.Environment; e != nil {
		fn.Environment = &types.Environment{
			Variables: e.Variables,
		}
	}

	if i := c.ImageConfigResponse; i != nil && i.ImageConfig != nil {
		fn.ImageConfig = &types.ImageConfig{
			Command:          i.ImageConfig.Command,
			EntryPoint:       i.ImageConfig.EntryPoint,
			WorkingDirectory: i.ImageConfig.WorkingDirectory,
		}
	}
	for _, layer := range c.Layers {
		fn.Layers = append(fn.Layers, *layer.Arn)
	}
	if t := c.TracingConfig; t != nil {
		fn.TracingConfig = &types.TracingConfig{
			Mode: t.Mode,
		}
	}
	if v := c.VpcConfig; v != nil && *v.VpcId != "" {
		fn.VpcConfig = &types.VpcConfig{
			SubnetIds:               v.SubnetIds,
			SecurityGroupIds:        v.SecurityGroupIds,
			Ipv6AllowedForDualStack: v.Ipv6AllowedForDualStack,
		}
	}

	if (code != nil && aws.ToString(code.RepositoryType) == "ECR") || fn.PackageType == types.PackageTypeImage {
		log.Printf("[debug] Image URL=%s", *code.ImageUri)
		fn.PackageType = types.PackageTypeImage
		fn.Code = &types.FunctionCode{
			ImageUri: code.ImageUri,
		}
	}

	fn.Tags = tags

	return fn
}

func fillDefaultValues(fn *Function) {
	if fn == nil {
		return
	}
	if len(fn.Architectures) == 0 {
		fn.Architectures = []types.Architecture{types.ArchitectureX8664}
	}
	if fn.Description == nil {
		fn.Description = aws.String("")
	}
	if fn.MemorySize == nil {
		fn.MemorySize = aws.Int32(128)
	}
	if fn.Layers == nil {
		fn.Layers = []string{}
	}
	if lc := fn.LoggingConfig; lc == nil {
		fn.LoggingConfig = &types.LoggingConfig{
			LogFormat: types.LogFormatText,
			LogGroup:  aws.String(resolveLogGroup(fn)),
		}
	} else {
		if lc.LogFormat == "" {
			lc.LogFormat = types.LogFormatText
		}
		if lc.LogGroup == nil {
			lc.LogGroup = aws.String(resolveLogGroup(fn))
		}
		if lc.ApplicationLogLevel == "" && lc.LogFormat == types.LogFormatJson {
			lc.ApplicationLogLevel = types.ApplicationLogLevelInfo
		}
		if lc.SystemLogLevel == "" && lc.LogFormat == types.LogFormatJson {
			lc.SystemLogLevel = types.SystemLogLevelInfo
		}
	}
	if fn.TracingConfig == nil {
		fn.TracingConfig = &types.TracingConfig{
			Mode: types.TracingModePassThrough,
		}
	}
	if fn.EphemeralStorage == nil {
		fn.EphemeralStorage = &types.EphemeralStorage{
			Size: aws.Int32(512),
		}
	}
	if fn.Timeout == nil {
		fn.Timeout = aws.Int32(3)
	}
	if fn.SnapStart == nil {
		fn.SnapStart = &types.SnapStart{
			ApplyOn: types.SnapStartApplyOnNone,
		}
	}
}

func newSnapStart(s *types.SnapStartResponse) *types.SnapStart {
	if s == nil {
		return nil
	}
	return &types.SnapStart{
		ApplyOn: s.ApplyOn,
	}
}

func exportEnvFile(file string) error {
	if file == "" {
		return nil
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	envs, err := envparse.Parse(f)
	if err != nil {
		return err
	}
	for key, value := range envs {
		os.Setenv(key, value)
	}
	return nil
}

var errCannotUpdateImageAndZip = fmt.Errorf("cannot update function code between Image and Zip")

func validateUpdateFunction(currentConf *types.FunctionConfiguration, currentCode *types.FunctionCodeLocation, newFn *Function) error {
	if currentConf == nil {
		// create new function
		return nil
	}

	newCode := newFn.Code

	// new=Image
	if newCode != nil && newCode.ImageUri != nil || newFn.PackageType == packageTypeImage {
		// current=Zip
		if currentCode == nil || currentCode.ImageUri == nil {
			return errCannotUpdateImageAndZip
		}
	}

	// current=Image
	if currentCode != nil && currentCode.ImageUri != nil || currentConf != nil && currentConf.PackageType == types.PackageTypeImage {
		// new=Zip
		if newCode == nil || newCode.ImageUri == nil {
			return errCannotUpdateImageAndZip
		}
	}

	return nil
}
