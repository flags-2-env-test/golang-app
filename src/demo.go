// Go consumer of oresoftware/flags-2-env.
//
// Asserts the contract in EXPECTED.md. Exits non-zero on the first
// disagreement, which is what makes `docker run` the whole test.
//
// Go statically compiles parser.c through cgo, so — like the C++ fixture and
// unlike everything else here — there is no shared object and nothing to
// resolve at runtime.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	flags2env "github.com/oresoftware/flags-2-env/clients/golang"
)

type testCase struct {
	label    string
	flags    []string
	expected map[string]string
}

func envMap(port, debug, appEnv, color string) map[string]string {
	return map[string]string{
		"PORT":    port,
		"DEBUG":   debug,
		"APP_ENV": appEnv,
		"COLOR":   color,
	}
}

func same(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func main() {
	config := ".cli-flags.toml"
	if len(os.Args) > 1 {
		config = os.Args[1]
	}

	defaults := envMap("3000", "false", "development", "true")
	overridden := envMap("8181", "true", "production", "true")
	negated := envMap("3000", "false", "development", "false")

	cases := []testCase{
		{"defaults", nil, defaults},
		{"long flags", []string{"--port", "8181", "--debug=t", "--mode", "production"}, overridden},
		{"short flags", []string{"-p", "8181", "-d", "1", "--env", "production"}, overridden},
		{"long aliases", []string{"--listen-port", "8181", "--debug", "1", "--mode", "production"}, overridden},
		{"joined by =", []string{"--port=8181", "--debug=yes", "--mode=production"}, overridden},
		{"negation", []string{"--no-color"}, negated},
	}

	failures := 0

	for _, tc := range cases {
		argv := append([]string{"demo"}, tc.flags...)

		got, err := flags2env.ParseFromFile(config, argv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: parse returned an error: %v\n", tc.label, err)
			failures++
			continue
		}

		ok := same(tc.expected, got)
		if !ok {
			failures++
		}

		status := "ok"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%-4s %-13s demo %s\n", status, tc.label, strings.Join(tc.flags, " "))

		for _, key := range sortedKeys(tc.expected) {
			value, present := got[key]
			if !present {
				value = "<missing>"
			}
			fmt.Printf("       %s=%s\n", key, value)
		}

		if !ok {
			fmt.Fprintf(os.Stderr, "       expected %v\n", tc.expected)
			fmt.Fprintf(os.Stderr, "       got      %v\n", got)
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\ngolang-app: %d of %d cases disagree with the contract\n", failures, len(cases))
		os.Exit(1)
	}

	fmt.Printf("\ngolang-app OK: %d cases, via cgo into oresoftware/flags-2-env\n", len(cases))
}
