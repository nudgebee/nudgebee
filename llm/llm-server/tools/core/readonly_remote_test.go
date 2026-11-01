package core

import "testing"

func TestExecRemoteCommandIsReadOnly(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
		want bool
	}{
		{"df is read-only", []string{"kubectl", "exec", "pod", "-n", "ns", "--", "df", "-h", "/data"}, true},
		{"grep is read-only", []string{"kubectl", "exec", "pod", "--", "grep", "ERROR", "log.txt"}, true},
		{"du is read-only", []string{"kubectl", "exec", "pod", "--", "du", "-sh", "/data"}, true},
		{"ping is read-only", []string{"kubectl", "exec", "pod", "--", "ping", "-c", "2", "10.0.0.1"}, true},
		{"nc -z is read-only", []string{"kubectl", "exec", "pod", "--", "nc", "-z", "-v", "backend-service", "80"}, true},
		{"nc without -z is not read-only", []string{"kubectl", "exec", "pod", "--", "nc", "host", "4444"}, false},
		{"nc -l is not read-only", []string{"kubectl", "exec", "pod", "--", "nc", "-l", "-p", "4444"}, false},
		{"curl GET is read-only", []string{"kubectl", "exec", "pod", "--", "curl", "-s", "http://x/health"}, true},
		{"curl -X POST is not read-only", []string{"kubectl", "exec", "pod", "--", "curl", "-X", "POST", "http://x"}, false},
		{"rm is not read-only", []string{"kubectl", "exec", "pod", "--", "rm", "-rf", "/data"}, false},
		{"no -- separator", []string{"kubectl", "exec", "pod", "ls"}, false},
		// Review (#38727): env runs whatever follows it.
		{"bare env is read-only", []string{"kubectl", "exec", "pod", "--", "env"}, true},
		{"env with a command is not read-only", []string{"kubectl", "exec", "pod", "--", "env", "rm", "-rf", "/data"}, false},
		{"env with assignment and shell is not read-only", []string{"kubectl", "exec", "pod", "--", "env", "FOO=1", "sh", "-c", "kill -9 1"}, false},
		{"date is not on the allowlist", []string{"kubectl", "exec", "pod", "--", "date", "-s", "2020-01-01"}, false},
		{"hostname is not on the allowlist", []string{"kubectl", "exec", "pod", "--", "hostname", "evil"}, false},
		{"top is not on the allowlist", []string{"kubectl", "exec", "pod", "--", "top"}, false},
		{"chained command is not read-only", []string{"kubectl", "exec", "pod", "--", "df", ";", "rm", "-rf", "/"}, false},
		{"shell wrapper is not read-only", []string{"kubectl", "exec", "pod", "--", "sh", "-c", "df -h"}, false},
		{"curl -o writes a file", []string{"kubectl", "exec", "pod", "--", "curl", "-s", "http://x", "-o", "/data/overwrite"}, false},
		{"curl bundled -so writes a file", []string{"kubectl", "exec", "pod", "--", "curl", "-so", "/data/x", "http://x"}, false},
		{"curl --output writes a file", []string{"kubectl", "exec", "pod", "--", "curl", "--output=/data/x", "http://x"}, false},
		{"curl -O writes a file", []string{"kubectl", "exec", "pod", "--", "curl", "-O", "http://x/big.tar"}, false},
		{"curl -s -i is read-only", []string{"kubectl", "exec", "pod", "--", "curl", "-s", "-i", "http://10.64.1.54:8080/readyz"}, true},
		{"wget -qO- is read-only", []string{"kubectl", "exec", "pod", "--", "wget", "-qO-", "http://x"}, true},
		{"wget -O - is read-only", []string{"kubectl", "exec", "pod", "--", "wget", "-O", "-", "http://x"}, true},
		{"wget saving a file is not read-only", []string{"kubectl", "exec", "pod", "--", "wget", "http://x/big.tar"}, false},
		{"wget -O file is not read-only", []string{"kubectl", "exec", "pod", "--", "wget", "-O", "/data/x", "http://x"}, false},
		{"rabbitmqctl list_exchanges is read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "list_exchanges"}, true},
		{"rabbitmqctl with node flag is read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "-n", "rabbit@h", "list_queues", "name"}, true},
		{"rabbitmqctl status is read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "status"}, true},
		{"rabbitmqctl cluster_status is read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "cluster_status"}, true},
		{"rabbitmqctl environment is read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "environment"}, true},
		{"rabbitmqctl eval stays gated", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "eval", "halt()."}, false},
		{"rabbitmqctl delete_queue stays gated", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "delete_queue", "q"}, false},
		{"rabbitmqctl with no subcommand is not read-only", []string{"kubectl", "exec", "pod", "--", "rabbitmqctl", "-q"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExecRemoteCommandIsReadOnly(c.cmd); got != c.want {
				t.Errorf("ExecRemoteCommandIsReadOnly(%v) = %v, want %v", c.cmd, got, c.want)
			}
		})
	}
}

func TestSSMPayloadIsReadOnly(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"systemctl status is read-only", `aws ssm send-command --parameters 'commands=["systemctl status nginx"]'`, true},
		{"systemctl restart is not read-only", `aws ssm send-command --parameters 'commands=["systemctl restart nginx"]'`, false},
		{"df is read-only", `aws ssm send-command --parameters 'commands=["df -h"]'`, true},
		{"compound payload is not inspectable", `aws ssm send-command --parameters 'commands=["df -h; rm -rf /"]'`, false},
		// Review (#38727): a list of commands must not be judged by its first entry.
		{"command list is not read-only", `aws ssm send-command --parameters 'commands=["df -h","rm -rf /var/lib/postgresql"]'`, false},
		{"unbracketed command list is not read-only", `aws ssm send-command --parameters commands="df -h","shutdown -h now"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SSMPayloadIsReadOnly(c.command); got != c.want {
				t.Errorf("SSMPayloadIsReadOnly(%q) = %v, want %v", c.command, got, c.want)
			}
		})
	}
}

func TestIsReadOnlyNcCommand(t *testing.T) {
	cases := []struct {
		name   string
		fields []string
		want   bool
	}{
		{"zero-I/O scan", []string{"nc", "-z", "-v", "backend-service", "80"}, true},
		{"no -z flag", []string{"nc", "host", "4444"}, false},
		{"listen flag rejected even with -z", []string{"nc", "-z", "-l", "4444"}, false},
		{"listen flag alone rejected", []string{"nc", "-l", "-p", "4444"}, false},
		{"bundled -zv with -w", []string{"nc", "-zv", "-w", "2", "host", "80"}, true},
		{"-w attached", []string{"nc", "-z", "-w2", "-n", "10.0.0.1", "80"}, true},
		// Review (#38727): -e/-c run a program on the connection.
		{"-e runs a program", []string{"nc", "-z", "-e", "/bin/sh", "host", "80"}, false},
		{"-c runs a shell command", []string{"nc", "-z", "-c", "sh", "host", "80"}, false},
		{"bundled -zc", []string{"nc", "-zc", "sh", "host", "80"}, false},
		{"bundled listen -zl", []string{"nc", "-zl", "4444"}, false},
		{"-p source port is not on the allowlist", []string{"nc", "-z", "-p", "4444", "host", "80"}, false},
		{"not nc", []string{"echo", "-z"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsReadOnlyNcCommand(c.fields); got != c.want {
				t.Errorf("IsReadOnlyNcCommand(%v) = %v, want %v", c.fields, got, c.want)
			}
		})
	}
}

func TestIsReadOnlyWgetCommand(t *testing.T) {
	cases := []struct {
		name   string
		fields []string
		want   bool
	}{
		{"plain GET to stdout", []string{"wget", "-qO-", "http://localhost:8429/api/v1/query"}, true},
		{"-q -O - with timeout", []string{"wget", "-q", "-O", "-", "-T", "5", "http://x"}, true},
		{"--output-document=- with --timeout=", []string{"wget", "--output-document=-", "--timeout=5", "--no-check-certificate", "http://x"}, true},
		{"post-data is a mutation", []string{"wget", "-qO-", "--post-data=x", "http://x"}, false},
		{"post-file is a mutation", []string{"wget", "-qO-", "--post-file=/tmp/x", "http://x"}, false},
		{"method PUT is a mutation (assigned form)", []string{"wget", "-qO-", "--method=PUT", "http://x"}, false},
		{"method POST is a mutation (space-separated form)", []string{"wget", "-qO-", "--method", "POST", "http://x"}, false},
		// Allowlist: anything not known to only read asks, even a harmless GET.
		{"--method GET is not on the allowlist", []string{"wget", "-qO-", "--method=GET", "http://x"}, false},
		{"--method with nothing after it", []string{"wget", "--method"}, false},
		{"saving a file is not read-only", []string{"wget", "-q", "http://x/big.tar"}, false},
		// Review (#38727): each of these writes a file in the pod.
		{"-o writes a log file", []string{"wget", "-qO-", "-o", "/app/log", "http://x"}, false},
		{"--output-file writes a log file", []string{"wget", "-qO-", "--output-file=/app/log", "http://x"}, false},
		{"--save-cookies writes a file", []string{"wget", "-qO-", "--save-cookies", "/app/c", "http://x"}, false},
		{"not wget", []string{"curl", "http://x"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsReadOnlyWgetCommand(c.fields); got != c.want {
				t.Errorf("IsReadOnlyWgetCommand(%v) = %v, want %v", c.fields, got, c.want)
			}
		})
	}
}

func TestIsReadOnlyCurlCommand(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain GET", "curl -s http://x/health", true},
		{"allowed flags, bundled and separate", "curl -sSik -L -f -m 5 --connect-timeout 3 -H Accept:json -u u:p http://x/readyz", true},
		{"bundled value flag", "curl -sSm5 http://x", true},
		{"--max-time=", "curl --silent --max-time=5 --write-out=%{http_code} http://x", true},
		{"POST via -X", "curl -X POST http://x", false},
		{"data flag", "curl -d foo http://x", false},
		{"bundled short mutation flag", "curl -sSXPOST http://x", false},
		{"-o writes a file", "curl -s http://x -o /data/x", false},
		{"bundled -so writes a file", "curl -so /data/x http://x", false},
		// Review (#38727): each of these writes or overwrites a file in the pod.
		{"-D dumps headers to a file", "curl -s http://x -D /app/h.txt", false},
		{"--dump-header", "curl -s http://x --dump-header /app/h.txt", false},
		{"-c writes a cookie jar", "curl -s http://x -c /app/jar", false},
		{"--cookie-jar", "curl -s http://x --cookie-jar /app/jar", false},
		{"--trace", "curl -s http://x --trace /app/t", false},
		{"--stderr", "curl -s http://x --stderr /app/config.yaml", false},
		{"--libcurl", "curl -s http://x --libcurl /app/c.c", false},
		{"--etag-save", "curl -s http://x --etag-save /app/e", false},
		{"-w %output{} writes a file", "curl -s -w %output{/app/x}%{http_code} http://x", false},
		{"value flag with no value", "curl -s http://x -m", false},
		{"lone dash", "curl - http://x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsReadOnlyCurlCommand(c.input); got != c.want {
				t.Errorf("IsReadOnlyCurlCommand(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}
