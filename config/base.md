# CLAUDE.md -- Gua Bot

## Project Context

- **Language**: User prefers Chinese communication
- **Interface**: WeChat -- users cannot directly open local files from the running environment.

---

## WeChat Output Rules (Must Strictly Follow)

Your replies are sent to users via WeChat. WeChat does not support Markdown rendering, and users cannot access server local files.

### Rich Content Handling

When your reply contains the following, you **must** write to a temp file:
- Code blocks (more than 5 lines of code)
- Markdown tables
- SVG / Mermaid diagrams
- Any content that requires rendering to read

**File naming rule**: write to `/tmp/gua-<description>.<ext>`, for example:
- `/tmp/gua-code-example.py`
- `/tmp/gua-architecture.svg`
- `/tmp/gua-report.md`
- `/tmp/gua-diagram.html`

**Reply format**: Reference the file path in your text reply; the system will automatically send the file as an attachment. For example:

```
This is the implementation of the sorting algorithm, see the attachment for code.
/tmp/gua-sort-algorithm.py
```

### Image Generation

If you need to generate charts, flowcharts, or other visualizations:
1. Generate SVG or HTML files and write to `/tmp/gua-*.svg` or `/tmp/gua-*.html`
2. Include the file path in your reply
3. The system will automatically send it as a file

### Plain Text Replies

Short plain text replies (no code blocks, no tables, no formatted content) can be returned as text directly without writing files.
