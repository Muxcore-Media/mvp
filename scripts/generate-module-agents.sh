#!/usr/bin/env bash
# Generate thin AGENTS.md for MuxCore module repos from muxcore.json.
set -euo pipefail
ROOT="/home/ender/Projects/MuxCore"
SKIP="muxcorectl|media-ui|cache-memory|custom-scripts|media-jellyfin|muxcore-module-starter/cookiecutter"

for dir in "$ROOT"/*/; do
  name=$(basename "$dir")
  echo "$name" | grep -Eq "^($SKIP)$" && continue
  [[ -f "$dir/muxcore.json" ]] || continue
  [[ -f "$dir/AGENTS.md" ]] && [[ "$name" == "media-ui-app" ]] && continue

  caps=$(python3 -c "import json; d=json.load(open('$dir/muxcore.json')); print(', '.join(d.get('capabilities',[])))" 2>/dev/null || echo "")
  modname=$(python3 -c "import json; print(json.load(open('$dir/muxcore.json')).get('name',''))" 2>/dev/null || echo "$name")
  contracts=$(python3 -c "
import json
d=json.load(open('$dir/muxcore.json'))
cs=d.get('contracts') or []
if not cs:
    print('none declared')
else:
    print('; '.join(f\"{c.get('interface')} ({c.get('repo','').split('/')[-1]})\" for c in cs))
" 2>/dev/null || echo "none declared")

  cat > "$dir/AGENTS.md" <<EOF
# AGENTS.md — $modname

MuxCore sidecar module (\`$name\`). Workspace deploy and SSH: [\`../AGENTS.md\`](../AGENTS.md). Default ports: [\`_mvp/PORTS.md\`](../_mvp/PORTS.md).

## Module identity

| Field | Value |
|-------|-------|
| Directory | \`$name\` |
| Capabilities | ${caps:-see muxcore.json} |
| Contracts | $contracts |

## Agent rules

- Modules run as gRPC sidecars; capabilities are the security boundary.
- TLS required in production (\`MUXCORE_INSECURE_DISABLE_TLS\` is dev-only).
- Match existing Go patterns; run \`gofmt\` and package tests before finishing.
- Cross-module events: prefer \`github.com/Muxcore-Media/contracts-media/events\` over deprecated \`core/pkg/contracts\` aliases.
- Do not edit polluted workspace dumps (see \`MASTER-ROADMAP.md\` Appendix H).

## Build

\`\`\`bash
cd $name
go test ./...
\`\`\`
EOF
  echo "wrote $dir/AGENTS.md"
done

# Go modules without muxcore.json but with go.mod at top level
for special in core muxcorectl-cli admin-ui _mvp; do
  [[ -d "$ROOT/$special" ]] || continue
done

echo "done"
