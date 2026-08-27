# Troubleshooting

## The server refuses to listen on a non-loopback address

This is intentional. Set a strong `WEAVEFLOW_MANAGEMENT_TOKEN` before using `-addr 0.0.0.0:8080`, or bind to a loopback
address while developing.

## The model request returns 404 or an unsupported-operation error

Check the API root and request format first:

```bash
curl "$OPENAI_BASE_URL/models" -H "Authorization: Bearer $OPENAI_API_KEY"
```

Many compatible endpoints require `/v1`. Match `chat_completions` versus `responses` to the provider. Confirm the model
ID independently before adding tools or long prompts.

## A Graph Definition fails validation

Fetch `GET /registry` and compare the definition with the current node, condition, State Module, capability, and reducer
contracts. Common causes are a misspelled type, a missing required port, a path in `config` instead of `state`, an
unknown State Module version, or overlapping parallel writes without a reducer.

## A Run pauses and will not resume

Inspect the Run status, last Checkpoint, and pending Step. Supply the required resume input or resolve the pending effect;
do not create a second Run until you know whether the original side effect completed. Resume only against the same Graph
Session revision.

## A tool is not found

List `GET /runtime/tools` and compare the returned IDs with the node's `tool_ids`. The server must install the tool in its
runtime context, and file tools additionally require a permitted workspace root.

## The Workbench cannot reach the API

Check the API origin, route prefix, and CORS allowlist. For a prefixed server use a base URL such as
`http://localhost:8080/debug`; for a different origin pass that exact origin to `-cors-origins`. Then reload the browser
and inspect the Network panel for the first failed request.

## Docker health check fails

Read the container logs and test the endpoint from inside the container:

```bash
docker logs weaveflow
docker exec weaveflow wget -q -O - http://127.0.0.1:8080/healthz
```

Check that the Web and server ports differ, `/tmp` is writable, and the data volume is mounted. The image expects a
prebuilt WebUI and uses `/tmp` for generated startup configuration.

## Where to look next

- [Runtime Model](/concepts/runtime) for lifecycle and checkpoint semantics.
- [Inspect and Operate Runs](/guides/observability) for evidence-first diagnosis.
- [Configuration Reference](/reference/configuration) for flags and environment variables.
- [Runnable Examples](/guides/examples) for small reproducible graphs.
