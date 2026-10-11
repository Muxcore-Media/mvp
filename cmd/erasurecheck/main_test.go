package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func snapshotJSON(t *testing.T, options protojson.MarshalOptions) (Expected, []byte, []byte) {
	t.Helper()
	e, stone, response := validSnapshots()
	stoneJSON, err := options.Marshal(stone)
	if err != nil {
		t.Fatal(err)
	}
	statusJSON, err := options.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return e, stoneJSON, statusJSON
}

func cliArgs(t *testing.T, e Expected, stone []byte) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tombstone.json")
	if err := os.WriteFile(path, stone, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"-erasure-id", e.ErasureID, "-user-id", e.UserID, "-tenant-id", e.TenantID,
		"-required", strings.Join(e.Required, ","), "-tombstone-file", path}
}

func TestRunValidProtoJSON(t *testing.T) {
	for _, snake := range []bool{false, true} {
		for _, numeric := range []bool{false, true} {
			t.Run(fmt.Sprintf("snake=%v numeric=%v", snake, numeric), func(t *testing.T) {
				e, stone, status := snapshotJSON(t, protojson.MarshalOptions{UseProtoNames: snake, UseEnumNumbers: numeric})
				e.Required[0] = " \t" + e.Required[0] + " "
				var stdout, stderr bytes.Buffer
				if rc := run(cliArgs(t, e, stone), bytes.NewReader(status), &stdout, &stderr); rc != 0 {
					t.Fatalf("exit=%d stderr=%s", rc, &stderr)
				}
				if stdout.String() != "OK snapshot acknowledgment check required=4\n" || stderr.Len() != 0 {
					t.Fatalf("unexpected output: %q / %q", &stdout, &stderr)
				}
			})
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	cases := map[string]string{
		"empty": "", "truncated": `{"erasures":[`, "null": `null`, "array": `[]`, "number": `1`, "string": `"object"`,
		"trailing document": `{} {}`, "trailing garbage": `{} garbage`, "unknown": `{"unknown":"SENTINEL"}`,
		"duplicate field":   `{"erasures":[],"erasures":[]}`,
		"alias conflict":    `{"nextPageToken":"","next_page_token":""}`,
		"escaped duplicate": `{"nextPageToken":"","nextPage\u0054oken":""}`,
		"nested duplicate":  `{"erasures":[{"erasureId":"e","erasureId":"e"}]}`,
		"nested aliases":    `{"erasures":[{"erasureId":"e","erasure_id":"e"}]}`,
		"module aliases":    `{"erasures":[{"modules":[{"moduleId":"a","module_id":"a"}]}]}`,
		"nested unknown":    `{"erasures":[{"modules":[{"unknown":"SENTINEL"}]}]}`,
		"null list":         `{"erasures":null}`, "null item": `{"erasures":[null]}`,
		"null string": `{"nextPageToken":null}`, "null bool": `{"erasures":[{"complete":null}]}`,
		"null module list": `{"erasures":[{"modules":null}]}`, "null module": `{"erasures":[{"modules":[null]}]}`,
		"object list": `{"erasures":{}}`, "wrong string": `{"nextPageToken":1}`,
		"wrong bool": `{"erasures":[{"complete":"true"}]}`, "number bool": `{"erasures":[{"complete":1}]}`,
		"invalid UTF8": "{\"nextPageToken\":\"\xff\"}", "lone surrogate": `{"nextPageToken":"\ud800"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if decodeSnapshot([]byte(raw), new(authv1.GetUserErasureStatusResponse)) {
				t.Fatal("accepted malformed proto-JSON")
			}
		})
	}
	for _, outcome := range []string{`null`, `true`, `false`, `1.0`, `1e0`, `-1`, `4`, `2147483648`, `"1"`, `"UNKNOWN"`, `{}`, `[]`} {
		t.Run("enum/"+outcome, func(t *testing.T) {
			raw := `{"erasures":[{"modules":[{"outcome":` + outcome + `}]}]}`
			if decodeSnapshot([]byte(raw), new(authv1.GetUserErasureStatusResponse)) {
				t.Fatal("accepted invalid enum token")
			}
		})
	}
	for name, raw := range map[string]string{
		"unknown": `{"unknown":"SENTINEL"}`, "aliases": `{"erasureId":"e","erasure_id":"e"}`,
		"null": `null`, "null scalar": `{"userId":null}`, "wrong bool": `{"acknowledgedByCaller":1}`,
		"wrong string": `{"userId":false}`, "trailing": `{} {}`,
	} {
		t.Run("tombstone/"+name, func(t *testing.T) {
			if decodeSnapshot([]byte(raw), new(authv1.UserErasure)) {
				t.Fatal("accepted malformed tombstone")
			}
		})
	}
}

func TestDecodeDefinedEnums(t *testing.T) {
	for number, name := range authv1.ErasureOutcome_name {
		for _, token := range []string{fmt.Sprint(number), fmt.Sprintf("%q", name)} {
			raw := `{"erasures":[{"modules":[{"outcome":` + token + `}]}]}`
			if !decodeSnapshot([]byte(raw), new(authv1.GetUserErasureStatusResponse)) {
				t.Fatalf("refused defined enum %s", token)
			}
		}
	}
}

func TestRunMissingSemanticFields(t *testing.T) {
	e, stone, _ := snapshotJSON(t, protojson.MarshalOptions{})
	for _, status := range []string{`{}`, `{"erasures":[]}`, `{"erasures":[{}]}`} {
		var stdout, stderr bytes.Buffer
		if run(cliArgs(t, e, stone), strings.NewReader(status), &stdout, &stderr) == 0 || stdout.Len() != 0 {
			t.Fatal("accepted absent semantic fields")
		}
	}
	_, stoneMessage, response := validSnapshots()
	response.Erasures[0].Complete = false
	status, err := protojson.Marshal(response) // false is absent in ordinary proto-JSON.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(status), "complete") {
		t.Fatal("fixture should omit complete")
	}
	var stdout, stderr bytes.Buffer
	if run(cliArgs(t, e, stone), bytes.NewReader(status), &stdout, &stderr) == 0 {
		t.Fatal("accepted absent complete")
	}
	stoneMessage.UserId = ""
	missingUser, err := protojson.Marshal(stoneMessage)
	if err != nil {
		t.Fatal(err)
	}
	if run(cliArgs(t, e, missingUser), bytes.NewReader(status), &stdout, &stderr) == 0 {
		t.Fatal("accepted missing tombstone user")
	}
}

func TestRunUsageAndReadFailures(t *testing.T) {
	e, stone, status := snapshotJSON(t, protojson.MarshalOptions{})
	args := cliArgs(t, e, stone)
	cases := map[string][]string{
		"no args": nil, "unknown flag": {"-SENTINEL_SECRET", "SENTINEL_SECRET"}, "missing value": {"-erasure-id"},
		"positional":           append(append([]string{}, args...), "SENTINEL_SECRET"),
		"missing file flag":    args[:len(args)-2],
		"missing file":         append(append([]string{}, args[:len(args)-1]...), filepath.Join(t.TempDir(), "SENTINEL_SECRET")),
		"unreadable directory": append(append([]string{}, args[:len(args)-1]...), t.TempDir()),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if run(input, bytes.NewReader(status), &stdout, &stderr) == 0 || stdout.Len() != 0 {
				t.Fatal("accepted invalid invocation")
			}
			assertSafeFailure(t, stderr.String())
		})
	}
	for _, csv := range []string{"", "a,", ",a", "a,,b", "a,a", "a, a ", "a/b"} {
		t.Run("required="+csv, func(t *testing.T) {
			bad := append([]string{}, args...)
			bad[7] = csv
			var stdout, stderr bytes.Buffer
			if rc := run(bad, bytes.NewReader(status), &stdout, &stderr); rc != 2 {
				t.Fatalf("exit=%d, want usage failure", rc)
			}
			assertSafeFailure(t, stderr.String())
		})
	}
	var stdout, stderr bytes.Buffer
	if run([]string{"-h"}, strings.NewReader(""), &stdout, &stderr) != 0 || !strings.HasPrefix(stdout.String(), "usage:") || stderr.Len() != 0 {
		t.Fatal("help failed")
	}
	stdout.Reset()
	stderr.Reset()
	if run(args, failingIO{}, &stdout, &stderr) == 0 {
		t.Fatal("accepted reader failure")
	}
	assertSafeFailure(t, stderr.String())
	stderr.Reset()
	if run(args, bytes.NewReader(status), failingIO{}, &stderr) == 0 {
		t.Fatal("accepted output failure")
	}
	assertSafeFailure(t, stderr.String())
}

type failingIO struct{}

func (failingIO) Read([]byte) (int, error)  { return 0, errors.New("SENTINEL_SECRET") }
func (failingIO) Write([]byte) (int, error) { return 0, errors.New("SENTINEL_SECRET") }

func assertSafeFailure(t *testing.T, stderr string) {
	t.Helper()
	if !strings.HasPrefix(stderr, "FAIL snapshot acknowledgment check ") || strings.Contains(stderr, "SENTINEL_SECRET") || len(stderr) > 100 || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("unsafe or unbounded failure: %q", stderr)
	}
}

func TestRunInputBounds(t *testing.T) {
	e, stone, status := snapshotJSON(t, protojson.MarshalOptions{})
	pad := func(data []byte, size int) []byte {
		return append(bytes.Clone(data), bytes.Repeat([]byte(" "), size-len(data))...)
	}
	for _, which := range []string{"tombstone", "stdin"} {
		for _, size := range []int{maxSnapshotBytes, maxSnapshotBytes + 1} {
			t.Run(fmt.Sprintf("%s/%d", which, size), func(t *testing.T) {
				inputStone, inputStatus := stone, status
				if which == "tombstone" {
					inputStone = pad(stone, size)
				} else {
					inputStatus = pad(status, size)
				}
				var stdout, stderr bytes.Buffer
				rc := run(cliArgs(t, e, inputStone), bytes.NewReader(inputStatus), &stdout, &stderr)
				if size == maxSnapshotBytes {
					if rc != 0 {
						t.Fatalf("exact limit rejected: %s", &stderr)
					}
				} else {
					if rc == 0 || stdout.Len() != 0 {
						t.Fatal("oversized input accepted")
					}
					assertSafeFailure(t, stderr.String())
				}
			})
		}
	}
}

func TestRunNeverEchoesSnapshots(t *testing.T) {
	e, stone, response := validSnapshots()
	e.ErasureID, e.UserID, e.TenantID = "SENTINEL_SECRET_ERASURE", "SENTINEL_SECRET_USER", "SENTINEL_SECRET_TENANT"
	stone.ErasureId, stone.UserId, stone.TenantId = e.ErasureID, e.UserID, e.TenantID
	response.Erasures[0].ErasureId = e.ErasureID
	response.Erasures[0].Modules[0].DetailCode = "SENTINEL_SECRET_TOKEN"
	marshal := func(message proto.Message) []byte {
		t.Helper()
		data, err := protojson.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	stoneJSON := marshal(stone)
	for _, mode := range []string{"valid", "semantic", "parser", "file parser"} {
		t.Run(mode, func(t *testing.T) {
			inputStone, inputStatus := stoneJSON, marshal(response)
			switch mode {
			case "semantic":
				inputStatus = bytes.Replace(inputStatus, []byte(`"complete":true`), []byte(`"complete":false`), 1)
			case "parser":
				inputStatus = []byte(`{"SENTINEL_SECRET":"SENTINEL_SECRET"}`)
			case "file parser":
				inputStone = []byte(`{"SENTINEL_SECRET":"SENTINEL_SECRET"}`)
			}
			var stdout, stderr bytes.Buffer
			rc := run(cliArgs(t, e, inputStone), bytes.NewReader(inputStatus), &stdout, &stderr)
			if mode == "valid" {
				if rc != 0 || stdout.String() != "OK snapshot acknowledgment check required=4\n" || stderr.Len() != 0 {
					t.Fatal("valid snapshot refused")
				}
			} else {
				if rc == 0 || stdout.Len() != 0 {
					t.Fatal("invalid secret-bearing snapshot accepted")
				}
				assertSafeFailure(t, stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "SENTINEL_SECRET") {
				t.Fatal("echoed input secret")
			}
		})
	}
}

// Exercise the real main/os.Exit boundary in a separate process without
// building another binary or needing any network or service.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("ERASURECHECK_TEST_PROCESS") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"erasurecheck"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		t.Fatal("missing process argument separator")
	}
	e, stone, status := snapshotJSON(t, protojson.MarshalOptions{})
	args := cliArgs(t, e, stone)
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			processArgs := append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)
			cmd := exec.Command(os.Args[0], processArgs...)
			cmd.Env = append(os.Environ(), "ERASURECHECK_TEST_PROCESS=1")
			if valid {
				cmd.Stdin = bytes.NewReader(status)
			} else {
				cmd.Stdin = strings.NewReader("SENTINEL_SECRET")
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if valid {
				if err != nil || stdout.String() != "OK snapshot acknowledgment check required=4\n" || stderr.Len() != 0 {
					t.Fatalf("process failed: %v %s", err, &stderr)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 {
					t.Fatalf("exit=%v stdout=%s", err, &stdout)
				}
				assertSafeFailure(t, stderr.String())
			}
		})
	}
}
