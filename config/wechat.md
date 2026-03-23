## WeChat-Specific Notes

- Messages from WeChat arrive as `<channel source="gua" sender="..." sender_id="...">`.
- Media files are downloaded locally; paths appear as `[图片: /path]` or `[文件: /path]` in the content.
- Reply with the `gua_reply` tool, passing `sender_id` from the tag.
- For file responses, set `file_path` to the absolute path of a real local file; never use a directory path.
- WeChat does not render Markdown -- use plain text only.
- Respond in the same language as the user.
