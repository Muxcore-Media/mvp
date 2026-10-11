// erasurecheck checks untrusted paired erasure snapshots without network or
// store access. A successful check is not evidence of live erasure completion.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const maxSnapshotBytes = 1 << 20

const usage = `usage: erasurecheck -erasure-id E -user-id U [-tenant-id T] -required CSV -tombstone-file PATH
Read one GetUserErasureStatusResponse proto-JSON snapshot from stdin and one
UserErasure snapshot from PATH (maximum 1 MiB each). Check supplied snapshots
only; provider provenance, current state and actual data erasure are unproven.`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func fail(stderr io.Writer, code string, exitCode int) int {
	// Only fixed internal codes reach this function. Never echo flag/parser,
	// filesystem or input errors: all may contain credentials or personal data.
	_, _ = fmt.Fprintln(stderr, "FAIL snapshot acknowledgment check "+code)
	return exitCode
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("erasurecheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var expected Expected
	fs.StringVar(&expected.ErasureID, "erasure-id", "", "expected erasure ID")
	fs.StringVar(&expected.UserID, "user-id", "", "expected immutable user ID")
	fs.StringVar(&expected.TenantID, "tenant-id", "", "expected tenant ID (empty for household)")
	requiredCSV := fs.String("required", "", "independent configured module IDs")
	tombstoneFile := fs.String("tombstone-file", "", "paired UserErasure snapshot")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, usage)
			return 0
		}
		return fail(stderr, "usage", 2)
	}
	if fs.NArg() != 0 || *tombstoneFile == "" {
		return fail(stderr, "usage", 2)
	}
	expected.Required = strings.Split(*requiredCSV, ",")
	if _, err := expectedModules(expected); err != nil {
		return fail(stderr, err.Error(), 2)
	}
	f, err := os.Open(*tombstoneFile) //nolint:gosec // explicitly selected read-only snapshot input
	if err != nil {
		return fail(stderr, "input_read", 1)
	}
	tombstoneJSON, readErr := readSnapshot(f)
	closeErr := f.Close()
	if readErr != nil {
		return fail(stderr, readErr.Error(), 1)
	}
	if closeErr != nil {
		return fail(stderr, "input_read", 1)
	}
	statusJSON, err := readSnapshot(stdin)
	if err != nil {
		return fail(stderr, err.Error(), 1)
	}
	tombstone := new(authv1.UserErasure)
	status := new(authv1.GetUserErasureStatusResponse)
	if !decodeSnapshot(tombstoneJSON, tombstone) || !decodeSnapshot(statusJSON, status) {
		return fail(stderr, "invalid_json", 1)
	}
	if err := Check(expected, tombstone, status); err != nil {
		return fail(stderr, err.Error(), 1)
	}
	if _, err := fmt.Fprintf(stdout, "OK snapshot acknowledgment check required=%d\n", len(expected.Required)); err != nil {
		return fail(stderr, "output_write", 1)
	}
	return 0
}

func readSnapshot(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSnapshotBytes+1))
	if err != nil {
		return nil, checkFailure("input_read")
	}
	if len(data) > maxSnapshotBytes {
		return nil, checkFailure("input_too_large")
	}
	return data, nil
}

func decodeSnapshot(data []byte, message proto.Message) bool {
	if !strictMessageJSON(data, message.ProtoReflect().Descriptor()) {
		return false
	}
	return protojson.Unmarshal(data, message) == nil
}

// protojson deliberately accepts null and numeric enum values it does not
// know. This preflight restricts inputs to the snapshot contract and rejects
// duplicate semantic fields, including escaped keys and snake/camel aliases.
func strictMessageJSON(data []byte, descriptor protoreflect.MessageDescriptor) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return false
		}
		name, ok := token.(string)
		if !ok {
			return false
		}
		field := descriptor.Fields().ByJSONName(name)
		if field == nil {
			field = descriptor.Fields().ByName(protoreflect.Name(name))
		}
		if field == nil || seen[field.Number()] {
			return false
		}
		seen[field.Number()] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return false
		}
		if field.IsList() {
			var values []json.RawMessage
			if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) || json.Unmarshal(raw, &values) != nil {
				return false
			}
			for _, value := range values {
				if !strictFieldJSON(value, field) {
					return false
				}
			}
		} else if !strictFieldJSON(raw, field) {
			return false
		}
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return errors.Is(err, io.EOF)
}

func strictFieldJSON(raw []byte, field protoreflect.FieldDescriptor) bool {
	raw = bytes.TrimSpace(raw)
	switch field.Kind() {
	case protoreflect.MessageKind:
		return strictMessageJSON(raw, field.Message())
	case protoreflect.StringKind:
		var value string
		return bytes.HasPrefix(raw, []byte(`"`)) && json.Unmarshal(raw, &value) == nil
	case protoreflect.BoolKind:
		return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
	case protoreflect.EnumKind:
		if bytes.HasPrefix(raw, []byte(`"`)) {
			var name string
			return json.Unmarshal(raw, &name) == nil && field.Enum().Values().ByName(protoreflect.Name(name)) != nil
		}
		// ParseInt rejects fractional/exponent forms and bool/null. Decode above
		// already rejects invalid JSON number spellings such as +1 or 01.
		value, err := strconv.ParseInt(string(raw), 10, 32)
		return err == nil && field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) != nil
	default:
		return false
	}
}
