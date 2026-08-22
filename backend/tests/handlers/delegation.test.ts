import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  buildDelegationToolResultContent,
  createDelegateTaskTool,
  executeWithDelegation,
  isAgentKnown,
  isCircularDelegation,
} from "../../handlers/delegation.ts";
import { globalRegistry } from "../../providers/registry.ts";
import type { ChatRequest } from "../../../shared/types.ts";

vi.mock("@anthropic-ai/sdk", () => {
  const mockCreate = vi.fn();

  class MockAnthropic {
    messages = { create: mockCreate };
    constructor(_options: { apiKey: string }) {}
  }

  return {
    default: MockAnthropic,
    __mockCreate: mockCreate,
  };
});

vi.mock("../../providers/registry.ts", () => ({
  globalRegistry: {
    getAgent: vi.fn(),
    getProviderForAgent: vi.fn(),
    getAllAgents: vi.fn(),
  },
}));

const mockProvider = {
  id: "test-provider",
  name: "Test Provider",
  type: "openai" as const,
  supportsImages: () => true,
  executeChat: vi.fn(),
};

async function collectStream(
  generator: AsyncGenerator<{ type: string; data?: unknown; error?: string }>,
): Promise<Array<{ type: string; data?: unknown; error?: string }>> {
  const results = [];
  for await (const chunk of generator) {
    results.push(chunk);
  }
  return results;
}

describe("delegation helpers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("builds tool result JSON with required fields", () => {
    const content = buildDelegationToolResultContent("tool_abc", "hello", false);
    const parsed = JSON.parse(content);

    expect(parsed).toEqual({
      type: "tool_result",
      is_error: false,
      content: "hello",
      tool_use_id: "tool_abc",
    });
  });

  it("creates delegate_task tool with worker agent enum", () => {
    const tool = createDelegateTaskTool([
      { id: "worker-a", name: "Worker A", description: "Does A things" },
    ]);

    expect(tool.name).toBe("delegate_task");
    expect(tool.input_schema.properties?.agent_id).toMatchObject({
      enum: ["worker-a"],
    });
    expect(tool.input_schema.required).toEqual(["agent_id", "instructions"]);
  });

  it("detects circular delegation", () => {
    expect(isCircularDelegation(["orchestrator"], "orchestrator", "worker-a")).toBe(
      false,
    );
    expect(isCircularDelegation(["orchestrator"], "orchestrator", "orchestrator")).toBe(
      true,
    );
    expect(isCircularDelegation(["orchestrator", "worker-a"], "worker-b", "worker-a")).toBe(
      true,
    );
  });

  it("checks agent availability from registry and request", () => {
    const request: ChatRequest = {
      message: "test",
      requestId: "req-1",
      availableAgents: [
        {
          id: "remote-agent",
          name: "Remote",
          description: "Remote worker",
          workingDirectory: "/tmp",
          apiEndpoint: "http://localhost:8080",
        },
      ],
    };

    vi.mocked(globalRegistry.getAgent).mockReturnValue(undefined);
    expect(isAgentKnown("remote-agent", request)).toBe(true);
    expect(isAgentKnown("missing-agent", request)).toBe(false);

    vi.mocked(globalRegistry.getAgent).mockReturnValue({
      id: "registry-agent",
      name: "Registry Agent",
      description: "From registry",
      provider: "openai",
    });
    expect(isAgentKnown("registry-agent", request)).toBe(true);
  });
});

describe("executeWithDelegation", () => {
  const originalApiKey = process.env.ANTHROPIC_API_KEY;

  beforeEach(() => {
    vi.clearAllMocks();
    process.env.ANTHROPIC_API_KEY = "test-api-key";

    vi.mocked(globalRegistry.getAllAgents).mockReturnValue([
      {
        id: "worker-a",
        name: "Worker A",
        description: "Worker agent A",
        provider: "test-provider",
      },
    ]);

    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) => {
      if (agentId === "orchestrator") {
        return {
          id: "orchestrator",
          name: "Orchestrator",
          description: "Coordinates workflows",
          provider: "anthropic",
          isOrchestrator: true,
        };
      }
      if (agentId === "worker-a") {
        return {
          id: "worker-a",
          name: "Worker A",
          description: "Worker agent A",
          provider: "test-provider",
        };
      }
      return undefined;
    });

    vi.mocked(globalRegistry.getProviderForAgent).mockImplementation((agentId) => {
      if (agentId === "worker-a") return mockProvider;
      return undefined;
    });
  });

  afterEach(() => {
    if (originalApiKey === undefined) {
      delete process.env.ANTHROPIC_API_KEY;
    } else {
      process.env.ANTHROPIC_API_KEY = originalApiKey;
    }
  });

  async function mockAnthropicToolUseResponse(
    toolUseId: string,
    agentId: string,
    instructions: string,
  ) {
    const { __mockCreate } = await import("@anthropic-ai/sdk") as {
      __mockCreate: ReturnType<typeof vi.fn>;
    };

    __mockCreate.mockResolvedValueOnce({
      async *[Symbol.asyncIterator]() {
        yield {
          type: "message_start",
          message: {
            id: "msg_1",
            type: "message",
            role: "assistant",
            model: "claude-sonnet-4-20250514",
            content: [],
            stop_reason: null,
            stop_sequence: null,
            usage: { input_tokens: 1, output_tokens: 1 },
          },
        };
        yield {
          type: "content_block_start",
          index: 0,
          content_block: {
            type: "tool_use",
            id: toolUseId,
            name: "delegate_task",
            input: "",
          },
        };
        yield {
          type: "content_block_delta",
          index: 0,
          delta: {
            type: "input_json_delta",
            partial_json: JSON.stringify({ agent_id: agentId, instructions }),
          },
        };
        yield { type: "content_block_stop", index: 0 };
        yield {
          type: "message_delta",
          delta: { stop_reason: "tool_use", stop_sequence: null },
          usage: { output_tokens: 5 },
        };
        yield { type: "message_stop" };
      },
    });
  }

  async function mockAnthropicTextResponse(text: string) {
    const { __mockCreate } = await import("@anthropic-ai/sdk") as {
      __mockCreate: ReturnType<typeof vi.fn>;
    };

    __mockCreate.mockResolvedValueOnce({
      async *[Symbol.asyncIterator]() {
        yield {
          type: "message_start",
          message: {
            id: "msg_2",
            type: "message",
            role: "assistant",
            model: "claude-sonnet-4-20250514",
            content: [],
            stop_reason: null,
            stop_sequence: null,
            usage: { input_tokens: 1, output_tokens: 1 },
          },
        };
        yield {
          type: "content_block_start",
          index: 0,
          content_block: { type: "text", text: "" },
        };
        yield {
          type: "content_block_delta",
          index: 0,
          delta: { type: "text_delta", text },
        };
        yield { type: "content_block_stop", index: 0 };
        yield {
          type: "message_delta",
          delta: { stop_reason: "end_turn", stop_sequence: null },
          usage: { output_tokens: 5 },
        };
        yield { type: "message_stop" };
      },
    });
  }

  it("runs sub-agent and feeds tool_result back before completing", async () => {
    await mockAnthropicToolUseResponse("tool_delegate_1", "worker-a", "Analyze the UI");
    await mockAnthropicTextResponse("The worker found several UX issues.");

    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      yield { type: "text", content: "UX analysis complete: improve contrast." };
      yield { type: "done" };
    });

    const request: ChatRequest = {
      message: "Coordinate a UX review",
      requestId: "req-delegate-1",
      availableAgents: [
        {
          id: "worker-a",
          name: "Worker A",
          description: "UX worker",
          workingDirectory: "/tmp/worker-a",
          apiEndpoint: "http://localhost:8081",
        },
      ],
    };

    const abortController = new AbortController();
    const stream = await collectStream(
      executeWithDelegation("orchestrator", request, abortController, false),
    );

    const assistantMessages = stream.filter(
      (chunk) =>
        chunk.type === "claude_json" &&
        (chunk.data as { type?: string })?.type === "assistant",
    );
    const toolUseBlocks = assistantMessages.flatMap((chunk) => {
      const message = (chunk.data as { message?: { content?: unknown[] } }).message;
      return (message?.content || []).filter(
        (block) => (block as { type?: string }).type === "tool_use",
      );
    });

    expect(toolUseBlocks).toHaveLength(1);
    expect((toolUseBlocks[0] as { id?: string }).id).toBe("tool_delegate_1");

    const toolResultMessage = stream.find(
      (chunk) =>
        chunk.type === "claude_json" &&
        (chunk.data as { type?: string })?.type === "user",
    );
    expect(toolResultMessage).toBeDefined();

    const toolResultBlock = (
      (toolResultMessage!.data as { message: { content: Array<{ tool_use_id?: string; content?: string }> } })
        .message.content[0]
    );
    expect(toolResultBlock.tool_use_id).toBe("tool_delegate_1");

    const parsedResult = JSON.parse(toolResultBlock.content || "{}");
    expect(parsedResult.is_error).toBe(false);
    expect(parsedResult.tool_use_id).toBe("tool_delegate_1");
    expect(parsedResult.content).toContain("UX analysis complete");

    expect(mockProvider.executeChat).toHaveBeenCalled();
    expect(stream.some((chunk) => chunk.type === "done")).toBe(true);
  });

  it("emits stream error and error tool_result for unknown agents", async () => {
    await mockAnthropicToolUseResponse(
      "tool_unknown",
      "missing-agent",
      "Do something",
    );
    await mockAnthropicTextResponse("I could not delegate that task.");

    const request: ChatRequest = {
      message: "Delegate to someone",
      requestId: "req-unknown",
      availableAgents: [],
    };

    vi.mocked(globalRegistry.getAllAgents).mockReturnValue([]);

    const abortController = new AbortController();
    const stream = await collectStream(
      executeWithDelegation("orchestrator", request, abortController, false),
    );

    const streamError = stream.find((chunk) => chunk.type === "error");
    expect(streamError?.error).toContain("Unknown agent");
    expect(streamError?.error).toContain("missing-agent");

    const toolResultMessage = stream.find(
      (chunk) =>
        chunk.type === "claude_json" &&
        (chunk.data as { type?: string })?.type === "user",
    );
    const parsedResult = JSON.parse(
      (
        (toolResultMessage!.data as {
          message: { content: Array<{ content?: string; tool_use_id?: string }> };
        }).message.content[0].content
      ) || "{}",
    );

    expect(parsedResult.is_error).toBe(true);
    expect(parsedResult.content).toContain("missing-agent");
    expect(parsedResult.tool_use_id).toBe("tool_unknown");
  });

  it("returns sub-agent errors via tool_result only", async () => {
    await mockAnthropicToolUseResponse("tool_fail", "worker-a", "Fail please");
    await mockAnthropicTextResponse("Delegation failed, continuing.");

    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      yield { type: "error", error: "Provider API failed" };
    });

    const request: ChatRequest = {
      message: "Delegate with failure",
      requestId: "req-fail",
    };

    const abortController = new AbortController();
    const stream = await collectStream(
      executeWithDelegation("orchestrator", request, abortController, false),
    );

    const providerStreamErrors = stream.filter(
      (chunk) => chunk.type === "error" && chunk.error?.includes("Provider API failed"),
    );
    expect(providerStreamErrors).toHaveLength(0);

    const toolResultMessage = stream.find(
      (chunk) =>
        chunk.type === "claude_json" &&
        (chunk.data as { type?: string })?.type === "user",
    );
    const parsedResult = JSON.parse(
      (
        (toolResultMessage!.data as {
          message: { content: Array<{ content?: string }> };
        }).message.content[0].content
      ) || "{}",
    );

    expect(parsedResult.is_error).toBe(true);
    expect(parsedResult.content).toContain("Provider API failed");
  });

  it("emits circular delegation stream error", async () => {
    await mockAnthropicToolUseResponse(
      "tool_circular",
      "orchestrator",
      "Delegate back",
    );
    await mockAnthropicTextResponse("Handled circular delegation.");

    const request: ChatRequest = {
      message: "Try circular delegation",
      requestId: "req-circular",
    };

    const abortController = new AbortController();
    const stream = await collectStream(
      executeWithDelegation("orchestrator", request, abortController, false),
    );

    const streamError = stream.find((chunk) => chunk.type === "error");
    expect(streamError?.error?.toLowerCase()).toContain("circular");
  });

  it("uses placeholder when sub-agent produces no text", async () => {
    await mockAnthropicToolUseResponse("tool_empty", "worker-a", "Stay quiet");
    await mockAnthropicTextResponse("Done.");

    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      yield { type: "done" };
    });

    const request: ChatRequest = {
      message: "Delegate silently",
      requestId: "req-empty",
    };

    const abortController = new AbortController();
    const stream = await collectStream(
      executeWithDelegation("orchestrator", request, abortController, false),
    );

    const toolResultMessage = stream.find(
      (chunk) =>
        chunk.type === "claude_json" &&
        (chunk.data as { type?: string })?.type === "user",
    );
    const parsedResult = JSON.parse(
      (
        (toolResultMessage!.data as {
          message: { content: Array<{ content?: string }> };
        }).message.content[0].content
      ) || "{}",
    );

    expect(parsedResult.is_error).toBe(false);
    expect(parsedResult.content).toContain("no textual output");
  });
});
