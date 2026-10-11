package main

import (
	"regexp"
	"strings"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

type checkFailure string

func (e checkFailure) Error() string { return string(e) }

const (
	errExpected   checkFailure = "invalid_expected"
	errBinding    checkFailure = "identity_binding"
	errStatus     checkFailure = "invalid_status"
	errTimestamp  checkFailure = "invalid_timestamp"
	errModules    checkFailure = "invalid_modules"
	errRequired   checkFailure = "required_set"
	errOutcome    checkFailure = "invalid_outcome"
	errIncomplete checkFailure = "incomplete"
)

var (
	moduleIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	erasureIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	utcTimePattern   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)
)

// Expected comes from the caller's independent fixture/configuration, never
// from the status response. IDs are compared exactly; module CSV parsing trims
// surrounding whitespace without changing case.
type Expected struct {
	ErasureID string
	UserID    string
	TenantID  string
	Required  []string
}

func expectedModules(e Expected) (map[string]struct{}, error) {
	if !erasureIDPattern.MatchString(e.ErasureID) || e.UserID == "" || len(e.UserID) > 256 ||
		strings.TrimSpace(e.UserID) != e.UserID || len(e.TenantID) > 256 || strings.TrimSpace(e.TenantID) != e.TenantID || len(e.Required) == 0 {
		return nil, errExpected
	}
	set := make(map[string]struct{}, len(e.Required))
	for _, id := range e.Required {
		id = strings.TrimSpace(id)
		if !moduleIDPattern.MatchString(id) {
			return nil, errExpected
		}
		if _, exists := set[id]; exists {
			return nil, errExpected
		}
		set[id] = struct{}{}
	}
	return set, nil
}

func parseUTC(value string) (time.Time, error) {
	// time.Parse alone accepts comma fractions and truncates excess precision.
	// Accept exactly the provider's UTC RFC3339 form, with nanosecond precision.
	if !utcTimePattern.MatchString(value) {
		return time.Time{}, errTimestamp
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errTimestamp
	}
	return parsed, nil
}

// Check validates paired snapshots only. It proves neither their source nor
// current provider state, and says nothing about the owners' actual data.
func Check(expected Expected, tombstone *authv1.UserErasure, response *authv1.GetUserErasureStatusResponse) error {
	required, err := expectedModules(expected)
	if err != nil {
		return err
	}
	if tombstone == nil || tombstone.GetErasureId() != expected.ErasureID ||
		tombstone.GetUserId() != expected.UserID || tombstone.GetTenantId() != expected.TenantID {
		return errBinding
	}
	if response == nil || response.GetNextPageToken() != "" || len(response.GetErasures()) != 1 || response.Erasures[0] == nil {
		return errStatus
	}
	status := response.Erasures[0]
	if status.GetErasureId() != expected.ErasureID {
		return errBinding
	}
	deleted, err := parseUTC(tombstone.GetDeletedAt())
	if err != nil {
		return err
	}
	statusDeleted, err := parseUTC(status.GetDeletedAt())
	if err != nil {
		return err
	}
	if !deleted.Equal(statusDeleted) {
		return errBinding
	}
	seen := make(map[string]struct{}, len(status.GetModules()))
	matched := 0
	for _, module := range status.GetModules() {
		if module == nil || !moduleIDPattern.MatchString(module.GetModuleId()) {
			return errModules
		}
		id := module.GetModuleId()
		if _, exists := seen[id]; exists {
			return errModules
		}
		seen[id] = struct{}{}
		switch module.GetOutcome() {
		case authv1.ErasureOutcome_ERASURE_OUTCOME_OK, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, authv1.ErasureOutcome_ERASURE_OUTCOME_UNSUPPORTED:
		default:
			return errOutcome
		}
		if _, err := parseUTC(module.GetAckedAt()); err != nil {
			return err
		}
		_, want := required[id]
		if module.GetRequired() != want {
			return errRequired
		}
		if want {
			matched++
			if module.GetOutcome() != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
				return errIncomplete
			}
		}
	}
	if matched != len(required) {
		return errRequired
	}
	if !status.GetComplete() {
		return errIncomplete
	}
	return nil
}
