# WeaveFlow website

The website build combines the static project homepage and the VitePress documentation site into one deployment
directory.

## Build everything

```bash
cd internal/website
bun run build
```

The build installs the locked documentation dependencies when needed and produces:

```text
internal/website/dist/
├─ index.html
├─ styles.css
├─ script.js
├─ icon.svg
└─ docs/
   ├─ index.html
   ├─ getting-started.html
   ├─ zh/
   │  ├─ index.html
   │  └─ getting-started.html
   └─ assets/
```

Deploy `dist/` as the document root for `weaveflow.space`. The English documentation is available under
`https://weaveflow.space/docs/`, and the Chinese documentation is available under
`https://weaveflow.space/docs/zh/`. The homepage follows the visitor's system language on first load and stores a
manual language selection in `localStorage`.

Example Caddy configuration:

```caddyfile
weaveflow.space {
    encode zstd gzip

    redir /docs /docs/ 308

    handle_path /docs/* {
        root * /var/www/weaveflow.space/docs
        try_files {path} {path}.html {path}/
        file_server
    }

    handle {
        root * /var/www/weaveflow.space
        file_server
    }
}
```
