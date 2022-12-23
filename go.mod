module github.com/fujiwara/lambroll

go 1.15

require (
	github.com/Songmu/prompter v0.5.0
	github.com/aereal/jsondiff v0.2.3
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/aws/aws-sdk-go v1.44.147
	github.com/fatih/color v1.13.0
	github.com/fujiwara/logutils v1.1.0
	github.com/fujiwara/tfstate-lookup v0.4.2
	github.com/go-test/deep v1.0.7
	github.com/google/go-jsonnet v0.17.0
	github.com/hashicorp/go-envparse v0.0.0-20200406174449-d9cfd743a15e
	github.com/itchyny/gojq v0.12.8
	github.com/kayac/go-config v0.6.0
	github.com/mattn/go-isatty v0.0.14
	github.com/olekukonko/tablewriter v0.0.5
	github.com/pkg/errors v0.9.1
	github.com/shogo82148/go-retry v1.1.0
)

replace github.com/aereal/jsondiff => ../../aereal/jsondiff
