#!/usr/bin/env bash
set -euo pipefail

if [ -z "${GEMINI_API_KEY:-}" ]; then
  echo "Error: GEMINI_API_KEY is not set" >&2
  exit 1
fi

git add .

DIFF=$(git diff --cached)

if [ -z "$DIFF" ]; then
  echo "Nothing to commit."
  exit 0
fi

PROMPT="You are an expert software engineer. Analyze the following git diff and write a commit message following the Conventional Commits specification.

Format:
  <type>[optional scope]: <description>

  [optional body]

  [optional footer(s)]

Types: fix, feat, build, chore, ci, docs, style, refactor, perf, test
- fix: patches a bug (PATCH in semver)
- feat: introduces a new feature (MINOR in semver)
- Append ! after type/scope or add 'BREAKING CHANGE:' footer for breaking changes (MAJOR in semver)

Rules:
- Description: short summary (max 72 chars total for first line), lowercase, no trailing period
- Body: optional, explain the what/why (not the how), wrap at 72 chars
- Footer: optional, use git trailer format (e.g. 'BREAKING CHANGE: ...', 'Refs: #123')
- Do NOT include any markdown formatting, code blocks, or extra commentary
- Output ONLY the commit message

Git diff:
\`\`\`
${DIFF}
\`\`\`"

ESCAPED_PROMPT=$(printf '%s' "$PROMPT" | python3 -c "import json,sys; print(json.dumps(sys.stdin.read()))")

RESPONSE=$(curl -s \
  "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=${GEMINI_API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{
    \"contents\": [{
      \"parts\": [{
        \"text\": ${ESCAPED_PROMPT}
      }]
    }],
    \"generationConfig\": {
      \"temperature\": 0.2,
      \"maxOutputTokens\": 512
    }
  }")

COMMIT_MSG=$(echo "$RESPONSE" | python3 -c "
import json, sys
data = json.load(sys.stdin)
try:
    print(data['candidates'][0]['content']['parts'][0]['text'].strip())
except (KeyError, IndexError):
    print('Error parsing response:', json.dumps(data), file=sys.stderr)
    sys.exit(1)
")

echo ""
echo "=== Generated commit message ==="
echo "$COMMIT_MSG"
echo "================================"
echo ""

read -r -p "Use this message? [Y/n] " confirm
confirm="${confirm:-Y}"

if [[ "$confirm" =~ ^[Yy]$ ]]; then
  git commit -m "$COMMIT_MSG"
  git push
  echo "Done."
else
  echo "Aborted. Changes remain staged."
  exit 1
fi
