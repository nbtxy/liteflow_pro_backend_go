# LLM Replay / Fixture 使用说明

## 1) 默认录像（RecordOnly）

服务默认使用 `RecordOnly`，会把每次调用按以下结构落盘：

```text
storage/llm_requests/<conversation_id>/<user_message_id>/
  00_<provider>_stream.json
  00_<provider>_stream.chunks.jsonl
  manifest.json
```

同一条 message 下，工具执行结果也会被保存：

```text
storage/llm_requests/<conversation_id>/<user_message_id>/
  00_tool_00_<tool_name>.json
  00_tool_01_<tool_name>.json
```

## 2) 回放模式（ReplayOnly）

```bash
export LLM_RECORD_MODE=replay
make dev
```

如需强制把本地请求绑定到线上某条 fixture（`MSG_ID` 使用 assistant 回复消息的 message_id）：

```bash
export LLM_REPLAY_FORCE_CONV_ID=<conversation_id>
export LLM_REPLAY_FORCE_MSG_ID=<assistant_message_id>
```

## 3) 回放行为

在 `replay` 模式下，agent loop 会优先读取 `*_tool_*.json`，
命中后直接使用已录制结果，不再真实执行工具。

## 4) 从线上同步 fixture

```bash
export REMOTE_HOST=ecs-user@1.2.3.4
bash scripts/repro/sync_llm_fixtures.sh --conv <conv_id>
bash scripts/repro/sync_llm_fixtures.sh --conv <conv_id> --msg <msg_id>
bash scripts/repro/sync_llm_fixtures.sh --from "2026-04-29 10:00" --to "2026-04-29 11:00"
```
