import { describe, it, expect, vi, beforeEach } from "vitest";
import { Context } from "hono";
import { handleMultiAgentChatRequest } from "../../handlers/multiAgentChat.ts";
import { globalRegistry } from "../../providers/registry.ts";
import { globalImageHandler } from "../../utils/imageHandling.ts";
import type { ChatRequest } from "../../../shared/types.ts";

// Mock the registry and image handler
vi.mock("../../providers/registry.ts", () => ({
  globalRegistry: {
    getProviderForAgent: vi.fn(),
    getAgent: vi.fn(),
  },
}));

vi.mock("../../utils/imageHandling.ts", () => ({
  globalImageHandler: {
    captureScreenshot: vi.fn(),
  },
}));

// Mock provider for testing
const mockProvider = {
  id: "test-provider",
  name: "Test Provider",
  type: "openai" as const,
  supportsImages: () => true,
  executeChat: vi.fn(),
};

const mockAgent = {
  id: "test-agent",
  name: "Test Agent",
  description: "Test agent for unit tests",
  provider: "test-provider",
  config: {
    temperature: 0.7,
    maxTokens: 1000,
  },
};

async function readStreamResponses(response: Response): Promise<Array<Record<string, unknown>>> {
  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let streamData = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    streamData += decoder.decode(value);
  }

  return streamData
    .split("\n")
    .filter((line) => line.trim())
    .map((line) => JSON.parse(line) as Record<string, unknown>);
}

describe("handleMultiAgentChatRequest", () => {
  let mockContext: Partial<Context>;
  let requestAbortControllers: Map<string, AbortController>;
  
  beforeEach(() => {
    vi.clearAllMocks();
    
    requestAbortControllers = new Map();
    
    mockContext = {
      req: {
        json: vi.fn(),
      } as any,
      var: {
        config: {
          debugMode: true,
        },
      } as any,
    };
    
    // Setup default mocks
    vi.mocked(globalRegistry.getProviderForAgent).mockReturnValue(mockProvider);
    vi.mocked(globalRegistry.getAgent).mockReturnValue(mockAgent);
  });
  
  it("should handle single agent mention", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent analyze this interface",
      requestId: "req-123",
      sessionId: "session-456",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    // Mock provider response
    const mockResponses = [
      { type: "text" as const, content: "I can see the interface has..." },
      { type: "done" as const },
    ];
    
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      for (const response of mockResponses) {
        yield response;
      }
    });
    
    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    expect(response).toBeInstanceOf(Response);
    expect(response.headers.get("Content-Type")).toBe("application/x-ndjson");
    
    // Verify provider was called with correct parameters
    expect(mockProvider.executeChat).toHaveBeenCalledWith(
      expect.objectContaining({
        message: "@test-agent analyze this interface",
        requestId: "req-123",
        sessionId: "session-456",
      }),
      expect.objectContaining({
        debugMode: true,
        temperature: 0.7,
        maxTokens: 1000,
      })
    );
  });
  
  it("should handle screen capture command", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent capture_screen",
      requestId: "req-capture",
      sessionId: "session-capture",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    // Mock successful screenshot capture
    vi.mocked(globalImageHandler.captureScreenshot).mockResolvedValue({
      success: true,
      imagePath: "/tmp/screenshot_123.png",
      imageData: "base64-image-data",
      metadata: {
        timestamp: "2023-01-01T00:00:00.000Z",
        format: "png",
        size: { width: 1920, height: 1080 },
      },
    });
    
    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    expect(globalImageHandler.captureScreenshot).toHaveBeenCalledWith({
      format: "png",
    });
    
    // Read the response stream
    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    let streamData = "";
    
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      streamData += decoder.decode(value);
    }
    
    const responses = streamData
      .split("\n")
      .filter(line => line.trim())
      .map(line => JSON.parse(line));
    
    // Should have connection ack, chat room message, completion message, and done
    expect(responses.length).toBeGreaterThanOrEqual(3);
    
    // Find chat room message
    const chatRoomMessage = responses.find(r => 
      r.data?.type === "chat_room_message"
    );
    expect(chatRoomMessage).toBeDefined();
    expect(chatRoomMessage.data.message.type).toBe("image");
    expect(chatRoomMessage.data.message.imageData).toBe("base64-image-data");
    
    // Find completion message
    const completionMessage = responses.find(r => 
      r.data?.content?.includes("SCREENSHOT_CAPTURED")
    );
    expect(completionMessage).toBeDefined();
  });
  
  it("should handle screenshot capture failure", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent capture_screen",
      requestId: "req-fail",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    // Mock failed screenshot capture
    vi.mocked(globalImageHandler.captureScreenshot).mockResolvedValue({
      success: false,
      error: "Screen capture failed: No display detected",
      metadata: {
        timestamp: "2023-01-01T00:00:00.000Z",
        format: "png",
      },
    });
    
    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    let streamData = "";
    
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      streamData += decoder.decode(value);
    }
    
    const responses = streamData
      .split("\n")
      .filter(line => line.trim())
      .map(line => JSON.parse(line));
    
    // Should have an error response
    const errorResponse = responses.find(r => r.type === "error");
    expect(errorResponse).toBeDefined();
    expect(errorResponse.error).toContain("Screenshot capture failed");
  });
  
  it("should handle unknown agent", async () => {
    const chatRequest: ChatRequest = {
      message: "@unknown-agent do something",
      requestId: "req-unknown",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    vi.mocked(globalRegistry.getProviderForAgent).mockReturnValue(undefined);
    
    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    let streamData = "";
    
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      streamData += decoder.decode(value);
    }
    
    const responses = streamData
      .split("\n")
      .filter(line => line.trim())
      .map(line => JSON.parse(line));
    
    const errorResponse = responses.find(r => r.type === "error");
    expect(errorResponse).toBeDefined();
    expect(errorResponse.error).toContain("Agent 'unknown-agent' not found");
  });
  
  it("should handle multi-agent orchestration", async () => {
    const chatRequest: ChatRequest = {
      message: "@agent1 @agent2 coordinate to analyze and improve the dashboard",
      requestId: "req-multi",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    // Mock orchestrator agent
    const orchestratorAgent = {
      id: "orchestrator",
      name: "Orchestrator",
      description: "Orchestrates multi-agent workflows",
      provider: "claude-code",
      isOrchestrator: true,
    };
    
    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) => {
      if (agentId === "orchestrator") return orchestratorAgent;
      return mockAgent;
    });
    
    // Mock orchestrator provider response
    const orchestratorResponses = [
      { type: "text" as const, content: "I'll coordinate between agent1 and agent2..." },
      { type: "done" as const },
    ];
    
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      for (const response of orchestratorResponses) {
        yield response;
      }
    });
    
    await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    // Should have called the orchestrator
    expect(mockProvider.executeChat).toHaveBeenCalled();
  });
  
  it("should handle provider errors gracefully", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent analyze interface",
      requestId: "req-error",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    // Mock provider error
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      yield { type: "error" as const, error: "Provider API failed" };
    });
    
    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    let streamData = "";
    
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      streamData += decoder.decode(value);
    }
    
    const responses = streamData
      .split("\n")
      .filter(line => line.trim())
      .map(line => JSON.parse(line));
    
    const errorResponse = responses.find(r => r.type === "error");
    expect(errorResponse).toBeDefined();
    expect(errorResponse.error).toBe("Provider API failed");
  });

  it("should run delegated work and feed its result back to the delegating agent", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent delegate this work",
      requestId: "req-delegate",
      sessionId: "session-delegate",
    };

    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) => {
      if (agentId === "worker") {
        return {
          ...mockAgent,
          id: "worker",
          workingDirectory: "/tmp/worker",
        };
      }
      return mockAgent;
    });

    vi.mocked(mockProvider.executeChat).mockImplementation(async function* (request) {
      if (request.message === chatRequest.message) {
        yield {
          type: "tool_use" as const,
          toolName: "delegate_task",
          toolInput: {
            agent_id: "worker",
            instructions: "complete the delegated work",
          },
          toolUseId: "tool-use-1",
        };
        yield { type: "done" as const };
        return;
      }

      if (request.message === "complete the delegated work") {
        yield { type: "text" as const, content: "delegated result" };
        yield { type: "done" as const };
        return;
      }

      yield { type: "text" as const, content: "continuing after delegation" };
      yield { type: "done" as const };
    });

    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    const responses = await readStreamResponses(response);
    const toolUse = responses.find((item) => {
      const data = item.data;
      if (typeof data !== "object" || data === null) return false;
      const message = (data as { message?: unknown }).message;
      if (typeof message !== "object" || message === null) return false;
      const content = (message as { content?: unknown }).content;
      if (!Array.isArray(content)) return false;
      return content.some(
        (contentItem) =>
          typeof contentItem === "object" &&
          contentItem !== null &&
          (contentItem as { type?: unknown }).type === "tool_use"
      );
    });
    const toolResult = responses.find((item) => {
      const data = item.data;
      if (typeof data !== "object" || data === null) return false;
      const message = (data as { message?: unknown }).message;
      if (typeof message !== "object" || message === null) return false;
      const content = (message as { content?: unknown }).content;
      if (!Array.isArray(content)) return false;
      return content.some(
        (contentItem) =>
          typeof contentItem === "object" &&
          contentItem !== null &&
          (contentItem as { type?: unknown }).type === "tool_result"
      );
    });

    expect(mockProvider.executeChat).toHaveBeenCalledTimes(3);
    expect(mockProvider.executeChat).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        message: "complete the delegated work",
        workingDirectory: "/tmp/worker",
      }),
      expect.anything()
    );

    const feedbackRequest = vi.mocked(mockProvider.executeChat).mock.calls[2][0];
    const feedback = JSON.parse(feedbackRequest.message) as {
      type: string;
      is_error: boolean;
      content: string;
      tool_use_id: string;
    };
    expect(feedback).toEqual({
      type: "tool_result",
      is_error: false,
      content: "delegated result",
      tool_use_id: "tool-use-1",
    });

    expect(toolUse).toBeDefined();
    expect(toolResult).toBeDefined();
    const toolUseContent = (
      (toolUse!.data as { message: { content: Array<{ id?: string }> } }).message
        .content
    )[0];
    const toolResultContent = (
      (toolResult!.data as {
        message: { content: Array<{ tool_use_id?: string; content?: string }> };
      }).message.content
    )[0];
    expect(toolUseContent.id).toBe("tool-use-1");
    expect(toolResultContent.tool_use_id).toBe(toolUseContent.id);
    expect(toolResultContent.content).toBe("delegated result");
  });

  it("should report an unknown delegated agent and feed the error back", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent delegate to an unknown agent",
      requestId: "req-unknown-delegate",
    };

    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    vi.mocked(globalRegistry.getProviderForAgent).mockImplementation((agentId) =>
      agentId === "unknown-agent" ? undefined : mockProvider
    );
    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) =>
      agentId === "unknown-agent" ? undefined : mockAgent
    );
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* (request) {
      if (request.message === chatRequest.message) {
        yield {
          type: "tool_use" as const,
          toolName: "delegate_task",
          toolInput: {
            agent_id: "unknown-agent",
            instructions: "do unavailable work",
          },
          toolUseId: "unknown-tool-use",
        };
        yield { type: "done" as const };
        return;
      }

      yield { type: "done" as const };
    });

    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    const responses = await readStreamResponses(response);
    const errorResponse = responses.find((item) => item.type === "error");
    const toolResult = responses.find((item) => {
      const data = item.data;
      if (typeof data !== "object" || data === null) return false;
      const message = (data as { message?: unknown }).message;
      if (typeof message !== "object" || message === null) return false;
      const content = (message as { content?: unknown }).content;
      if (!Array.isArray(content)) return false;
      return content.some(
        (contentItem) =>
          typeof contentItem === "object" &&
          contentItem !== null &&
          (contentItem as { type?: unknown }).type === "tool_result"
      );
    });

    expect(errorResponse).toBeDefined();
    expect(errorResponse?.error).toContain("unknown-agent");
    expect(toolResult).toBeDefined();

    const feedbackRequest = vi.mocked(mockProvider.executeChat).mock.calls[1][0];
    const feedback = JSON.parse(feedbackRequest.message) as {
      is_error: boolean;
      content: string;
      tool_use_id: string;
    };
    expect(feedback.is_error).toBe(true);
    expect(feedback.content).toContain("unknown-agent");
    expect(feedback.tool_use_id).toBe("unknown-tool-use");
  });

  it("should feed a sub-agent failure back without a stream-level error", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent delegate to a failing agent",
      requestId: "req-failed-delegate",
    };

    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) => {
      if (agentId === "failing-agent") {
        return { ...mockAgent, id: "failing-agent" };
      }
      return mockAgent;
    });
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* (request) {
      if (request.message === chatRequest.message) {
        yield {
          type: "tool_use" as const,
          toolName: "delegate_task",
          toolInput: {
            agent_id: "failing-agent",
            instructions: "fail this work",
          },
          toolUseId: "failed-tool-use",
        };
        yield { type: "done" as const };
        return;
      }

      if (request.message === "fail this work") {
        yield { type: "error" as const, error: "sub-agent failed" };
        return;
      }

      yield { type: "done" as const };
    });

    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    const responses = await readStreamResponses(response);
    const errorResponse = responses.find((item) => item.type === "error");
    const feedbackRequest = vi.mocked(mockProvider.executeChat).mock.calls[2][0];
    const feedback = JSON.parse(feedbackRequest.message) as {
      is_error: boolean;
      content: string;
    };

    expect(errorResponse).toBeUndefined();
    expect(feedback.is_error).toBe(true);
    expect(feedback.content).toBe("sub-agent failed");
  });

  it("should stop circular delegation with a stream-level error", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent start a circular delegation",
      requestId: "req-circular",
    };

    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    vi.mocked(globalRegistry.getAgent).mockImplementation((agentId) => {
      if (agentId === "worker") {
        return { ...mockAgent, id: "worker" };
      }
      return mockAgent;
    });
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* (request) {
      const isRootRequest = request.message === chatRequest.message;
      yield {
        type: "tool_use" as const,
        toolName: "delegate_task",
        toolInput: {
          agent_id: isRootRequest ? "worker" : "test-agent",
          instructions: "continue the circular delegation",
        },
        toolUseId: isRootRequest ? "root-tool-use" : "worker-tool-use",
      };
      yield { type: "done" as const };
    });

    const response = await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    const responses = await readStreamResponses(response);
    const errorResponse = responses.find((item) => item.type === "error");

    expect(errorResponse).toBeDefined();
    expect(errorResponse?.error).toContain("circular");
    expect(
      responses.some((item) => {
        const data = item.data;
        if (typeof data !== "object" || data === null) return false;
        const message = (data as { message?: unknown }).message;
        if (typeof message !== "object" || message === null) return false;
        const content = (message as { content?: unknown }).content;
        return (
          Array.isArray(content) &&
          content.some(
            (contentItem) =>
              typeof contentItem === "object" &&
              contentItem !== null &&
              (contentItem as { type?: unknown }).type === "tool_result"
          )
        );
      })
    ).toBe(false);
  });
  
  it("should manage abort controllers correctly", async () => {
    const chatRequest: ChatRequest = {
      message: "@test-agent test request",
      requestId: "req-abort-test",
    };
    
    vi.mocked(mockContext.req!.json).mockResolvedValue(chatRequest);
    
    vi.mocked(mockProvider.executeChat).mockImplementation(async function* () {
      yield { type: "text" as const, content: "Response" };
      yield { type: "done" as const };
    });
    
    await handleMultiAgentChatRequest(
      mockContext as Context,
      requestAbortControllers
    );
    
    // Abort controller should be cleaned up
    expect(requestAbortControllers.has("req-abort-test")).toBe(false);
  });
});