package testutil

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"text/template"
	"unicode"

	"github.com/pachyderm/pachyderm/v2/src/client"
	"github.com/pachyderm/pachyderm/v2/src/internal/require"
)

// dedent is a helper function that trims repeated, leading spaces from several
// lines of text. This is useful for long, inline commands that are indented to
// match the surrounding code but should be interpreted without the space:
// e.g.:
// func() {
//   BashCmd(`
//     cat <<EOF
//     This is a test
//     EOF
//   `)
// }
// Should evaluate:
// $ cat <<EOF
// > This is a test
// > EOF
// not:
// $      cat <<EOF
// >      This is a test
// >      EOF  # doesn't terminate heredoc b/c of space
// such that the heredoc doesn't end properly
func dedent(cmd string) string {
	notSpace := func(r rune) bool {
		return !unicode.IsSpace(r)
	}

	prefix := "-" // non-space character indicates no prefix has been established
	s := bufio.NewScanner(strings.NewReader(cmd))
	var dedentedCmd bytes.Buffer
	for s.Scan() {
		i := strings.IndexFunc(s.Text(), notSpace)
		if i == -1 {
			continue // blank line (or spaces-only line)--ignore completely
		} else if prefix == "-" {
			prefix = s.Text()[:i] // first non-blank line, set prefix
			dedentedCmd.WriteString(s.Text()[i:])
			dedentedCmd.WriteRune('\n')
		} else if strings.HasPrefix(s.Text(), prefix) {
			dedentedCmd.WriteString(s.Text()[len(prefix):]) // remove prefix
			dedentedCmd.WriteRune('\n')
		} else {
			fmt.Fprintf(os.Stderr,
				"\nWARNING: the line\n  %q\ncannot be dedented as missing the prefix %q\n",
				s.Text(), prefix)
			return cmd // no common prefix--break early
		}
	}
	return dedentedCmd.String()
}

// Cmd is a convenience function that replaces exec.Command. It's both shorter
// and it uses the current process's stderr as output for the command, which
// makes debugging failures much easier (i.e. you get an error message
// rather than "exit status 1")
func Cmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	// for convenience, simulate hitting "enter" after any prompt. This can easily
	// be replaced
	cmd.Stdin = strings.NewReader("\n")
	cmd.Env = os.Environ()
	return cmd
}

func bashCmd(cmd string, subs ...string) io.Reader {
	if len(subs)%2 == 1 {
		panic("some variable does not have a corresponding value")
	}

	// copy 'subs' into a map
	subsMap := make(map[string]string)
	for i := 0; i < len(subs); i += 2 {
		subsMap[subs[i]] = subs[i+1]
	}

	// Warn users that they must install 'match' if they want to run tests with
	// this library, and enable 'pipefail' so that if any 'match' in a chain
	// fails, the whole command fails.
	buf := &bytes.Buffer{}
	buf.WriteString(`
set -e -o pipefail
# Try to ignore pipefail errors (encountered when writing to a closed pipe).
# Processes like 'yes' are essentially guaranteed to hit this, and because of
# -e -o pipefail they will crash the whole script. We need these options,
# though, for 'match' to work, so for now we work around pipefail errors on a
# cmd-by-cmd basis. See "The Infamous SIGPIPE Signal"
# http://www.tldp.org/LDP/lpg/node20.html
pipeerr=141 # typical error code returned by unix utils when SIGPIPE is raised
function yes {
	/usr/bin/yes || test "$?" -eq "${pipeerr}"
}
export -f yes # use in subshells too
which match >/dev/null || {
	echo "You must have 'match' installed to run these tests. Please run:" >&2
	echo "  go install ./src/testing/match" >&2
	exit 1
}`)
	buf.WriteRune('\n')

	// do the substitution
	template.Must(template.New("").Parse(dedent(cmd))).Execute(buf, subsMap)
	return buf
}

// BashCmd is a convenience function that:
// 1. Performs a Go template substitution on 'cmd' using the strings in 'subs'
// 2. Returns a command that runs the result string from 1 as a Bash script
func BashCmd(cmd string, subs ...string) *exec.Cmd {
	res := exec.Command("/bin/bash")
	res.Stderr = os.Stderr
	// useful for debugging, but makes logs too noisy:
	// res.Stdout = os.Stdout
	res.Stdin = bashCmd(cmd, subs...)
	res.Env = os.Environ()
	return res
}
func PachctlBashCmd(t *testing.T, c *client.APIClient, cmd string, subs ...string) *exec.Cmd {
	t.Helper()

	buf := new(bytes.Buffer)
	buf.ReadFrom(bashCmd(cmd, subs...))

	config := fmt.Sprintf("test-pach-config-%s.json", t.Name())
	if _, err := os.Open(config); err != nil {
		_, err = os.Create(config)
		require.NoError(t, err)
		// remove the empty file so that a config can be generated
		require.NoError(t, os.Remove(config))
		t.Cleanup(func() {
			// since this call gets run multiple times, ignore error
			_ = os.Remove(config)
		})
		return BashCmd(`
		export PACH_CONFIG={{.config}}
		pachctl config set context  --overwrite {{ .context }} <<EOF
		{
		  "source": 2,
		  "session_token": "{{.token}}",
		  "pachd_address": "grpc://{{.host}}:{{.port}}",
		  "cluster_deployment_id": "dev"
		}
		EOF
		pachctl config set active-context {{ .context }}
		{{.cmd}}
		`,
			"config", config,
			"context", t.Name(),
			"token", c.AuthToken(),
			"host", c.GetAddress().Host,
			"port", fmt.Sprint(c.GetAddress().Port),
			"cmd", buf.String(),
		)
	}
	return BashCmd(`
	export PACH_CONFIG={{.config}}
	pachctl config set active-context {{ .context }}
	{{.cmd}}
	`,
		"config", config,
		"context", t.Name(),
		"cmd", buf.String(),
	)
}
