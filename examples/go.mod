// The examples are their OWN module, deliberately.
//
// The root module's promise is that it has no third-party dependencies, and
// `task deps` enforces it. An example that demonstrates source-vault or
// format-yaml has to import that module — so if examples lived in the root
// module, every adapter an example touched would land in the dependency graph
// of every program that imports cfgkit. Splitting them keeps the guarantee
// intact and costs nothing: nobody imports the examples.
module github.com/ubgo/cfgkit/examples

go 1.26.4

require (
	filippo.io/age v1.2.1
	github.com/aws/aws-sdk-go-v2 v1.46.0
	github.com/aws/aws-sdk-go-v2/service/appconfigdata v1.30.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.111.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.48.0
	github.com/aws/aws-sdk-go-v2/service/ssm v1.77.0
	github.com/nats-io/nats-server/v2 v2.12.3
	github.com/nats-io/nats.go v1.53.1
	github.com/spf13/pflag v1.0.9
	github.com/thunderbottom/kiln v1.0.3
	github.com/ubgo/cfgkit v0.0.0-20260910135930-6a3f1f034569
	github.com/ubgo/cfgkit/contrib/flags-pflag v0.0.0
	github.com/ubgo/cfgkit/contrib/format-hcl v0.0.0
	github.com/ubgo/cfgkit/contrib/format-ini v0.0.0
	github.com/ubgo/cfgkit/contrib/format-properties v0.0.0
	github.com/ubgo/cfgkit/contrib/format-toml v0.0.0
	github.com/ubgo/cfgkit/contrib/format-yaml v0.0.0
	github.com/ubgo/cfgkit/contrib/source-appconfig v0.0.0
	github.com/ubgo/cfgkit/contrib/source-azurekeyvault v0.0.0
	github.com/ubgo/cfgkit/contrib/source-consul v0.0.0
	github.com/ubgo/cfgkit/contrib/source-etcd v0.0.0
	github.com/ubgo/cfgkit/contrib/source-gcpsecrets v0.0.0
	github.com/ubgo/cfgkit/contrib/source-k8s v0.0.0
	github.com/ubgo/cfgkit/contrib/source-kiln v0.0.0
	github.com/ubgo/cfgkit/contrib/source-nats v0.0.0
	github.com/ubgo/cfgkit/contrib/source-s3 v0.0.0
	github.com/ubgo/cfgkit/contrib/source-secretsmanager v0.0.0
	github.com/ubgo/cfgkit/contrib/source-ssm v0.0.0
	github.com/ubgo/cfgkit/contrib/source-vault v0.0.0
)
