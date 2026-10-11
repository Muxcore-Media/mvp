package main

import (
	"slices"
	"strings"
	"testing"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

const deletedAt = "2026-10-11T10:20:30Z"

func validSnapshots() (Expected, *authv1.UserErasure, *authv1.GetUserErasureStatusResponse) {
	expected := Expected{ErasureID: "erasure-fixture", UserID: "victim-fixture", TenantID: "tenant-fixture",
		Required: []string{"admin-ui", "media-ui", "request-media", "userdata-local"}}
	tombstone := &authv1.UserErasure{ErasureId: expected.ErasureID, UserId: expected.UserID, TenantId: expected.TenantID, DeletedAt: deletedAt}
	status := &authv1.ErasureStatus{ErasureId: expected.ErasureID, DeletedAt: deletedAt, Complete: true}
	for _, id := range expected.Required {
		status.Modules = append(status.Modules, acknowledged(id, true, authv1.ErasureOutcome_ERASURE_OUTCOME_OK))
	}
	return expected, tombstone, &authv1.GetUserErasureStatusResponse{Erasures: []*authv1.ErasureStatus{status}}
}

func acknowledged(id string, required bool, outcome authv1.ErasureOutcome) *authv1.ErasureModuleStatus {
	return &authv1.ErasureModuleStatus{ModuleId: id, Required: required, Outcome: outcome, AckedAt: "2026-10-11T10:21:00Z"}
}

type snapshotEdit func(*Expected, *authv1.UserErasure, *authv1.GetUserErasureStatusResponse)

func TestCheckValid(t *testing.T) {
	cases := map[string]snapshotEdit{
		"default four": func(_ *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {},
		"enabled eight reordered": func(e *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			for _, id := range []string{"playback-monitor", "playback-guard", "jellyfin", "media-library-maintainer"} {
				e.Required = append(e.Required, id)
				r.Erasures[0].Modules = append(r.Erasures[0].Modules, acknowledged(id, true, authv1.ErasureOutcome_ERASURE_OUTCOME_OK))
			}
			slices.Reverse(r.Erasures[0].Modules)
		},
		"empty household tenant": func(e *Expected, stone *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.TenantID, stone.TenantId = "", ""
		},
		"expected module whitespace and case preserved": func(e *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			e.Required[0], r.Erasures[0].Modules[0].ModuleId = " \tAdmin-UI \n", "Admin-UI"
		},
		"optional outcomes": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			for _, outcome := range []authv1.ErasureOutcome{authv1.ErasureOutcome_ERASURE_OUTCOME_OK, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, authv1.ErasureOutcome_ERASURE_OUTCOME_UNSUPPORTED} {
				r.Erasures[0].Modules = append(r.Erasures[0].Modules, acknowledged("optional-"+outcome.String(), false, outcome))
			}
		},
		"equivalent deletion instant": func(_ *Expected, stone *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			stone.DeletedAt, r.Erasures[0].DeletedAt = "2026-10-11T10:20:30.000000000Z", "2026-10-11T10:20:30.0Z"
		},
		"no clock or acknowledgment order policy": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules[0].AckedAt = "2000-01-01T00:00:00Z"
		},
		"caller hint ignored": func(_ *Expected, stone *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			stone.AcknowledgedByCaller = true
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			e, stone, response := validSnapshots()
			edit(&e, stone, response)
			if err := Check(e, stone, response); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckRejects(t *testing.T) {
	cases := map[string]snapshotEdit{
		"empty required": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) { e.Required = nil },
		"empty member": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required = append(e.Required, "")
		},
		"whitespace member": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required[0] = " \t"
		},
		"duplicate required": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required = append(e.Required, e.Required[0])
		},
		"duplicate required after trim": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required = append(e.Required, " "+e.Required[0]+" ")
		},
		"invalid required": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required[0] = "module/invalid"
		},
		"long required": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required[0] = strings.Repeat("a", 65)
		},
		"empty expected erasure": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) { e.ErasureID = "" },
		"invalid expected erasure": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.ErasureID = "bad/id"
		},
		"empty expected user": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) { e.UserID = "" },
		"wrong tombstone erasure": func(_ *Expected, s *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			s.ErasureId = "other"
		},
		"wrong immutable user": func(_ *Expected, s *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			s.UserId = "bystander"
		},
		"user comparison exact": func(_ *Expected, s *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) { s.UserId += " " },
		"wrong tenant":          func(_ *Expected, s *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) { s.TenantId = "other" },
		"missing erasure":       func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) { r.Erasures = nil },
		"multiple erasures": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures = append(r.Erasures, r.Erasures[0])
		},
		"nil erasure": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) { r.Erasures[0] = nil },
		"wrong status erasure": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].ErasureId = "other"
		},
		"continuation": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.NextPageToken = "more"
		},
		"different deletion instant": func(_ *Expected, s *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			s.DeletedAt = "2026-10-11T10:20:30.000000001Z"
		},
		"nil module": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules[0] = nil
		},
		"invalid module ID": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules[0].ModuleId = "module/invalid"
		},
		"duplicate required row": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules = append(r.Erasures[0].Modules, r.Erasures[0].Modules[0])
		},
		"duplicate optional row": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			optional := acknowledged("optional", false, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED)
			r.Erasures[0].Modules = append(r.Erasures[0].Modules, optional, optional)
		},
		"missing required row": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules = r.Erasures[0].Modules[1:]
		},
		"unexpected required row": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules = append(r.Erasures[0].Modules, acknowledged("unexpected", true, authv1.ErasureOutcome_ERASURE_OUTCOME_OK))
		},
		"required reported optional": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Modules[0].Required = false
		},
		"required case mismatch": func(e *Expected, _ *authv1.UserErasure, _ *authv1.GetUserErasureStatusResponse) {
			e.Required[0] = "Admin-UI"
		},
		"complete false": func(_ *Expected, _ *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			r.Erasures[0].Complete = false
		},
		"complete alone": func(_ *Expected, s *authv1.UserErasure, r *authv1.GetUserErasureStatusResponse) {
			s.AcknowledgedByCaller = true
			r.Erasures[0].Modules = nil
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			e, stone, response := validSnapshots()
			edit(&e, stone, response)
			if err := Check(e, stone, response); err == nil {
				t.Fatal("accepted invalid snapshots")
			}
		})
	}
	for _, which := range []string{"tombstone", "response"} {
		t.Run("nil "+which, func(t *testing.T) {
			e, stone, response := validSnapshots()
			if which == "tombstone" {
				stone = nil
			} else {
				response = nil
			}
			if Check(e, stone, response) == nil {
				t.Fatal("accepted nil snapshot")
			}
		})
	}
}

func TestCheckOutcomes(t *testing.T) {
	for _, outcome := range []authv1.ErasureOutcome{0, 2, 3, -1, 4, 2147483647} {
		t.Run(outcome.String(), func(t *testing.T) {
			e, stone, response := validSnapshots()
			response.Erasures[0].Modules[0].Outcome = outcome
			if Check(e, stone, response) == nil {
				t.Fatal("accepted non-OK required outcome")
			}
		})
	}
	for _, outcome := range []authv1.ErasureOutcome{0, -1, 4} {
		t.Run("optional "+outcome.String(), func(t *testing.T) {
			e, stone, response := validSnapshots()
			response.Erasures[0].Modules = append(response.Erasures[0].Modules, acknowledged("optional", false, outcome))
			if Check(e, stone, response) == nil {
				t.Fatal("accepted invalid optional outcome")
			}
		})
	}
}

func TestCheckRefusesVacuousCompletion(t *testing.T) {
	for _, withOptional := range []bool{false, true} {
		e, stone, response := validSnapshots()
		e.Required = nil
		response.Erasures[0].Modules = nil
		if withOptional {
			response.Erasures[0].Modules = []*authv1.ErasureModuleStatus{acknowledged("optional", false, authv1.ErasureOutcome_ERASURE_OUTCOME_OK)}
		}
		if err := Check(e, stone, response); err != errExpected {
			t.Fatalf("vacuous completion: got %v, want invalid expected set", err)
		}
	}
}

func TestCheckRejectsInvalidBoundExpectations(t *testing.T) {
	for _, field := range []string{"erasure", "user", "tenant"} {
		values := []string{" leading", "trailing ", "em\x00bedded", "line\nbreak", "unicode\u0085control", "invalid\xff", strings.Repeat("x", 257)}
		if field != "tenant" {
			values = append(values, "")
		}
		if field == "erasure" {
			values = append(values, "bad/id", strings.Repeat("x", 129))
		}
		for _, value := range values {
			t.Run(field+"/"+value, func(t *testing.T) {
				e, stone, response := validSnapshots()
				// Match all corresponding inputs so only validity, not a binding
				// mismatch, can reject this malformed expectation.
				switch field {
				case "erasure":
					e.ErasureID, stone.ErasureId, response.Erasures[0].ErasureId = value, value, value
				case "user":
					e.UserID, stone.UserId = value, value
				case "tenant":
					e.TenantID, stone.TenantId = value, value
				}
				if err := Check(e, stone, response); err != errExpected {
					t.Fatalf("got %v, want invalid expectation", err)
				}
			})
		}
	}
}

func TestCheckTimestamps(t *testing.T) {
	for _, value := range []string{"", "invalid", "2026-02-30T10:20:30Z", "2026-10-11T10:20:60Z", "2026-10-11T10:20:30+01:00", "2026-10-11T10:20:30+00:00", "2026-10-11T10:20:30-00:00", "2026-10-11T1:20:30Z", "2026-10-11T10:20:30,1Z", "2026-10-11T10:20:30.0000000001Z"} {
		for _, field := range []string{"tombstone", "status", "required ack", "optional ack"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				e, stone, response := validSnapshots()
				switch field {
				case "tombstone":
					stone.DeletedAt = value
				case "status":
					response.Erasures[0].DeletedAt = value
				case "required ack":
					response.Erasures[0].Modules[0].AckedAt = value
				case "optional ack":
					optional := acknowledged("optional", false, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED)
					optional.AckedAt = value
					response.Erasures[0].Modules = append(response.Erasures[0].Modules, optional)
				}
				if Check(e, stone, response) == nil {
					t.Fatal("accepted invalid timestamp")
				}
			})
		}
	}
}
