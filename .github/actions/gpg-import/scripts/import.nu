# Imports a GPG private key for signing and writes its fingerprint to $GITHUB_OUTPUT. The key comes from the
# PRIVATE_KEY environment variable and must not have a passphrase/
#
# A failing external command stops the script with its exit code.

# values of the given type (e.g. "fpr") from gpg's --with-colons output, the 10th field
def colon-fields [type: string]: string -> list<string> {
	lines
	| each {|line| $line | split row ":" }
	| where {|fields| ($fields | first) == $type }
	| each {|fields| $fields | get 9 }
}

let gpg_home = $env.GNUPGHOME? | default ($env.HOME | path join ".gnupg")
mkdir $gpg_home
^chmod 700 $gpg_home

$env.PRIVATE_KEY | ^gpg --batch --quiet --import

let fingerprint = ^gpg --batch --list-secret-keys --with-colons | colon-fields "fpr" | get 0?
if $fingerprint == null {
	error make { msg: "no secret key found after import" }
}

$"fingerprint=($fingerprint)\n" | save --append $env.GITHUB_OUTPUT
