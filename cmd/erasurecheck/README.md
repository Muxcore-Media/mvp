# Erasure snapshot checker

`erasurecheck` validates a supplied status snapshot against a paired tombstone
and an independently supplied expectation (T-M4-07 E12b, ADR-0035).

```sh
erasurecheck -erasure-id E -user-id U -tenant-id '' \
  -required admin-ui,media-ui,request-media,userdata-local \
  -tombstone-file tombstone.json < status.json
```

`status.json` must be one `GetUserErasureStatusResponse` proto-JSON object;
`tombstone.json` must be one `UserErasure` object, not an exported ledger or a
list response. Each input is limited to 1 MiB. The expected erasure, immutable
user and tenant IDs must match the paired inputs. The status has no user ID:
the mandatory tombstone supplies that association. An omitted tenant flag means
the empty household tenant.

The required CSV must come from the caller's independently selected configuration
(the household generator is `scripts/gen-erasure-set.sh`). Whitespace around
CSV entries is trimmed; case is preserved. Empty, duplicate or invalid module
IDs are refused. Status rows marked required must match this set exactly,
each must have `ERASURE_OUTCOME_OK` and an acknowledgment timestamp, and
`complete` must be true. Unique optional rows with `OK`, `FAILED` or
`UNSUPPORTED` acknowledgments are allowed and do not count toward this set.

Proto-JSON camelCase and original snake_case field names are accepted, as are
named or defined integer enums. Unknown fields, duplicate keys or aliases,
nulls, wrong types, fractional enums and trailing documents are refused.
All deletion and acknowledgment times must use UTC RFC3339 with `Z` and at
most nine fractional digits. Deletion times must represent the same instant;
there is no wall-clock, acknowledgment ordering or retention policy.

Success prints only `OK snapshot acknowledgment check required=N`. Failures
print a fixed diagnostic code, never IDs, paths or raw input. Exit codes are
0 for a valid snapshot (or help), 1 for input/verification/output failure, and
2 for invalid command usage or expectations.
Flags follow Go's command parser: repeated flags use the last supplied value.

This command performs no network requests, authentication, acknowledgment
writes, deletion or restore. A successful check does not establish snapshot
provenance, current provider state, any owner's actual data disposition, or
full household erasure acceptance. A future adapter must establish those
properties separately; `Check` is the pure validation seam it can reuse.
