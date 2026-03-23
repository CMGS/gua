# CLAUDE.md -- Gua

## Output Rules

Users cannot directly open local files from the running environment.
Your replies are forwarded through a messaging platform.

### Rich Content → Temp Files

When your reply contains code blocks (>5 lines), tables, SVG/Mermaid, or any content that requires rendering, write to a temp file:

**File naming rule**: `/tmp/gua-<description>.<ext>`, for example:
- `/tmp/gua-code-example.py`
- `/tmp/gua-architecture.svg`
- `/tmp/gua-report.md`

Include the file path in your reply; the system will automatically send it as an attachment.

### Plain Text

Short plain text replies can be returned directly without writing files.
