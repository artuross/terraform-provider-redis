# Generates the registry documentation in docs/ with tfplugindocs, from the schema of the provider built in bin/
# (`moon run build`, Terraform/OpenTofu finds it through TF_CLI_CONFIG_FILE). TERRAFORM_BINARY picks the binary,
# OpenTofu (`tofu`) by default.
#
# --check renders into bin/docs instead and fails if docs/ is out of date.
#
# The snippets in examples/provider and examples/resources/* are validated first.

# so the locally built provider is used, see TF_CLI_CONFIG_FILE
const required_providers = "terraform {\n  required_providers {\n    redis = { source = \"artuross/redis\" }\n  }\n}\n"

def main [--check] {
	let binary = $env.TERRAFORM_BINARY? | default "tofu"
	validate-snippets $binary

	let tmp = mktemp --directory
	let schema = $tmp | path join "schema.json"

	$required_providers | save ($tmp | path join "main.tf")

	# tfplugindocs only finds the provider under "redis" or "registry.terraform.io/hashicorp/redis", OpenTofu names it
	# registry.opentofu.org/artuross/redis
	^$binary $"-chdir=($tmp)" providers schema -json
	| from json
	| update provider_schemas {|schemas| { redis: ($schemas.provider_schemas | values | first) } }
	| to json
	| save $schema

	# relative to the repository, tfplugindocs doesn't accept absolute paths
	let output = if $check { "bin/docs" } else { "docs" }
	if $check and ($output | path exists) {
		rm --recursive $output
	}

	^tfplugindocs generate --provider-name redis --rendered-provider-name Redis --providers-schema $schema --rendered-website-dir $output
	rm --recursive $tmp

	if $check {
		let result = ^diff --recursive --unified docs $output | complete
		if $result.exit_code != 0 {
			print $result.stdout
			error make { msg: "docs/ is out of date, run `moon run docs`" }
		}
	}
}

# Validates the documentation snippets, they're rendered into the docs and must work when copied. The provider snippet
# is validated together with each resource's, a provider block on its own isn't checked.
def validate-snippets [binary: string] {
	for dir in (ls examples/resources | where type == dir | get name) {
		let tmp = mktemp --directory
		# -L follows symlinks, e.g. Lua files shared with the runnable examples
		^cp -RL examples/provider/. $"($dir)/." $tmp
		$required_providers | save ($tmp | path join "versions_test.tf")

		let result = ^$binary $"-chdir=($tmp)" validate -no-color | complete
		rm --recursive $tmp

		if $result.exit_code != 0 {
			print $result.stdout $result.stderr
			error make { msg: $"examples/provider with ($dir) is not valid configuration" }
		}
	}
}
