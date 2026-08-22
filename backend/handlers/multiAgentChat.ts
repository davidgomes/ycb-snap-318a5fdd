import { Context } from "hono";
import type { ChatRequest, StreamResponse } from "../../shared/types.ts";
import { globalRegistry } from "../providers/registry.ts";
import { globalImageHandler } from "../utils/imageHandling.ts";
import type { 
  ProviderChatRequest, 
  ProviderResponse, 
  ChatRoomMessage,
  AgentCommand 
} from "../providers/types.ts";

/**
 * Parse structured commands from chat messages
 */
function parseAgentCommand(message: string): AgentCommand | null {
  // Look for structured commands like: @claude-impl capture screenshot of /dashboard
  const commandMatch = message.match(/@[\w-]+ (capture_screen|analyze_image|implement_changes|review_code)(?:\s+(.+))?/);
  
  if (commandMatch) {
    const [, command, target] = commandMatch;
    return {
      command: command as AgentCommand["command"],
      target: target?.trim(),
    };
  }
  
  return null;
}

/**
 * Create a chat room message from agent response
 */
function createChatRoomMessage(
  response: ProviderResponse,
  agentId: string
): ChatRoomMessage | null {
  const timestamp = new Date().toISOString();
  
  switch (response.type) {
    case "text":
      return {
        type: "text",
        content: response.content || "",
        agentId,
        timestamp,
      };
      
    case "image":
      return {
        type: "image",
        content: response.content || "Image captured",
        imageData: response.imageData,
        agentId,
        timestamp,
      };
      
    case "tool_use":
      if (response.toolName === "capture_screen") {
        return {
          type: "command",
          content: `Executing screen capture: ${response.toolName}`,
          agentId,
          timestamp,
          metadata: {
            command: response.toolName,
          },
        };
      }
      break;
      
    case "error":
      return {
        type: "text",
        content: `Error: ${response.error}`,
        agentId,
        timestamp,
      };
  }
  
  return null;
}

interface DelegationInput {
  agentId: string;
  instructions: string;
}

interface ToolResult {
  type: "tool_result";
  is_error: boolean;
  content: string;
  tool_use_id: string;
}

interface AgentRunResult {
  accumulatedText: string;
  error?: string;
  circular?: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseDelegationInput(input: unknown): DelegationInput | null {
  let parsedInput = input;

  if (typeof parsedInput === "string") {
    try {
      parsedInput = JSON.parse(parsedInput);
    } catch {
      return null;
    }
  }

  if (!isRecord(parsedInput)) {
    return null;
  }

  const agentId = parsedInput.agent_id;
  const instructions = parsedInput.instructions;

  if (typeof agentId !== "string" || typeof instructions !== "string") {
    return null;
  }

  return { agentId, instructions };
}

function createToolUseStreamResponse(
  response: ProviderResponse,
  toolUseId: string,
  request: ChatRequest
): StreamResponse {
  return {
    type: "claude_json",
    data: {
      type: "assistant",
      message: {
        role: "assistant",
        content: [
          {
            type: "tool_use",
            id: toolUseId,
            name: response.toolName,
            input: response.toolInput,
          },
        ],
      },
      session_id: request.sessionId,
    },
  };
}

function createToolResultStreamResponse(
  toolResult: ToolResult,
  request: ChatRequest
): StreamResponse {
  return {
    type: "claude_json",
    data: {
      type: "user",
      message: {
        role: "user",
        content: [toolResult],
      },
      session_id: request.sessionId,
    },
  };
}

/**
 * Execute multi-agent chat with provider abstraction
 */
async function* executeMultiAgentChat(
  request: ChatRequest,
  requestAbortControllers: Map<string, AbortController>,
  debugMode: boolean = false
): AsyncGenerator<StreamResponse> {
  try {
    // Create abort controller
    const abortController = new AbortController();
    requestAbortControllers.set(request.requestId, abortController);
    
    if (debugMode) {
      console.debug("[Multi-Agent] Processing request:", {
        message: request.message.substring(0, 100) + "...",
        availableAgents: request.availableAgents?.map(a => a.id),
      });
    }
    
    // Parse agent mentions and commands
    const mentionMatches = request.message.match(/@([\w-]+)/g);
    const command = parseAgentCommand(request.message);
    
    if (mentionMatches && mentionMatches.length === 1) {
      // Single agent mention - direct execution
      const mentionedAgentId = mentionMatches[0].substring(1);
      
      if (debugMode) {
        console.debug(`[Multi-Agent] Single agent mentioned: ${mentionedAgentId}`);
      }
      
      yield* executeSingleAgent(
        mentionedAgentId,
        request,
        command,
        abortController,
        debugMode
      );
    } else {
      // Multi-agent or orchestration scenario
      yield* executeOrchestration(
        request,
        command,
        abortController,
        debugMode
      );
    }
    
  } catch (error) {
    yield {
      type: "error",
      error: error instanceof Error ? error.message : String(error),
    };
  } finally {
    requestAbortControllers.delete(request.requestId);
  }
}

/**
 * Execute chat with a single agent
 */
async function* executeAgent(
  agentId: string,
  message: string,
  request: ChatRequest,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: readonly string[],
  streamOutput: boolean,
  isRootAgent: boolean
): AsyncGenerator<StreamResponse, AgentRunResult> {
  const provider = globalRegistry.getProviderForAgent(agentId);
  const agentConfig = globalRegistry.getAgent(agentId);

  if (!provider || !agentConfig) {
    yield {
      type: "error",
      error: `Agent '${agentId}' not found or provider not available`,
    };
    return {
      accumulatedText: "",
      error: `Agent '${agentId}' not found or provider not available`,
    };
  }

  let accumulatedText = "";
  let nextMessage = message;
  let generatedToolUseCount = 0;

  while (true) {
    let delegatedToolResult: ToolResult | null = null;
    let providerError: string | undefined;

    const providerRequest: ProviderChatRequest = {
      message: nextMessage,
      sessionId: request.sessionId,
      requestId: request.requestId,
      workingDirectory: isRootAgent
        ? request.workingDirectory || agentConfig.workingDirectory
        : agentConfig.workingDirectory,
    };

    try {
      for await (const response of provider.executeChat(providerRequest, {
        debugMode,
        abortController,
        temperature: agentConfig.config?.temperature,
        maxTokens: agentConfig.config?.maxTokens,
      })) {
        if (streamOutput) {
          const chatRoomMessage = createChatRoomMessage(response, agentId);

          if (chatRoomMessage) {
            yield {
              type: "claude_json",
              data: {
                type: "chat_room_message",
                message: chatRoomMessage,
                session_id: request.sessionId,
              },
            };
          }
        }

        if (response.type === "text") {
          accumulatedText += response.content || "";

          if (streamOutput) {
            yield {
              type: "claude_json",
              data: {
                type: "assistant",
                content: response.content,
                model: response.metadata?.model,
              },
            };
          }
        } else if (response.type === "tool_use") {
          if (response.toolName !== "delegate_task" || delegatedToolResult) {
            continue;
          }

          const toolUseId =
            response.toolUseId ||
            `${request.requestId}-delegate-${++generatedToolUseCount}`;

          if (streamOutput) {
            yield createToolUseStreamResponse(response, toolUseId, request);
          }

          const delegation = parseDelegationInput(response.toolInput);
          if (!delegation) {
            delegatedToolResult = {
              type: "tool_result",
              is_error: true,
              content: "delegate_task requires agent_id and instructions",
              tool_use_id: toolUseId,
            };
            continue;
          }

          if (delegationPath.includes(delegation.agentId)) {
            yield {
              type: "error",
              error: `circular delegation detected: ${delegation.agentId} is already in the delegation chain`,
            };
            return {
              accumulatedText,
              circular: true,
            };
          }

          const subAgentResult = yield* executeAgent(
            delegation.agentId,
            delegation.instructions,
            request,
            abortController,
            debugMode,
            [...delegationPath, delegation.agentId],
            false,
            false
          );

          if (subAgentResult.circular) {
            return {
              accumulatedText,
              circular: true,
            };
          }

          const resultContent = subAgentResult.error
            ? subAgentResult.error
            : subAgentResult.accumulatedText.trim()
              ? subAgentResult.accumulatedText
              : "Sub-agent completed without textual output.";

          delegatedToolResult = {
            type: "tool_result",
            is_error: Boolean(subAgentResult.error),
            content: resultContent,
            tool_use_id: toolUseId,
          };
        } else if (response.type === "error") {
          providerError = response.error || "Agent provider failed";
          break;
        }
      }
    } catch (error) {
      providerError = error instanceof Error ? error.message : String(error);
    }

    if (providerError) {
      if (streamOutput) {
        yield { type: "error", error: providerError };
      }

      return {
        accumulatedText,
        error: providerError,
      };
    }

    if (delegatedToolResult) {
      if (streamOutput) {
        yield createToolResultStreamResponse(delegatedToolResult, request);
      }

      nextMessage = JSON.stringify(delegatedToolResult);
      continue;
    }

    if (streamOutput) {
      yield { type: "done" };
    }

    return { accumulatedText };
  }
}

async function* executeSingleAgent(
  agentId: string,
  request: ChatRequest,
  command: AgentCommand | null,
  abortController: AbortController,
  debugMode: boolean
): AsyncGenerator<StreamResponse> {
  if (command?.command === "capture_screen") {
    yield* handleScreenCapture(agentId, request, command, abortController, debugMode);
    return;
  }

  yield* executeAgent(
    agentId,
    request.message,
    request,
    abortController,
    debugMode,
    [agentId],
    true,
    true
  );
}

/**
 * Handle screen capture command
 */
async function* handleScreenCapture(
  agentId: string,
  request: ChatRequest,
  command: AgentCommand,
  abortController: AbortController,
  debugMode: boolean
): AsyncGenerator<StreamResponse> {
  try {
    if (debugMode) {
      console.debug(`[Multi-Agent] Handling screen capture for agent: ${agentId}`);
    }
    
    // Capture screenshot
    const capture = await globalImageHandler.captureScreenshot({
      format: "png",
    });
    
    if (!capture.success) {
      yield {
        type: "error",
        error: `Screenshot capture failed: ${capture.error}`,
      };
      return;
    }
    
    // Create chat room message for screenshot
    const chatRoomMessage: ChatRoomMessage = {
      type: "image",
      content: `Screenshot captured: ${capture.metadata.timestamp}`,
      imageData: capture.imageData,
      agentId,
      timestamp: new Date().toISOString(),
    };
    
    yield {
      type: "claude_json",
      data: {
        type: "chat_room_message",
        message: chatRoomMessage,
        session_id: request.sessionId,
      },
    };
    
    // Also yield a completion message
    yield {
      type: "claude_json",
      data: {
        type: "assistant",
        content: `📸 **SCREENSHOT_CAPTURED**\n\nI've captured a screenshot of the current interface. The image is now available for analysis by other agents in the chat room.\n\nImage details:\n- Format: ${capture.metadata.format}\n- Timestamp: ${capture.metadata.timestamp}\n- Size: ${capture.metadata.size?.width}x${capture.metadata.size?.height}`,
      },
    };
    
    yield { type: "done" };
    
  } catch (error) {
    yield {
      type: "error",
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

/**
 * Execute orchestration for multi-agent scenarios
 */
async function* executeOrchestration(
  request: ChatRequest,
  command: AgentCommand | null,
  abortController: AbortController,
  debugMode: boolean
): AsyncGenerator<StreamResponse> {
  // For now, delegate to orchestrator agent
  const orchestratorAgent = globalRegistry.getAgent("orchestrator");
  
  if (orchestratorAgent) {
    yield* executeSingleAgent(
      "orchestrator",
      request,
      command,
      abortController,
      debugMode
    );
  } else {
    yield {
      type: "error",
      error: "Orchestrator agent not available for multi-agent coordination",
    };
  }
}

/**
 * Main handler for multi-agent chat requests
 */
export async function handleMultiAgentChatRequest(
  c: Context,
  requestAbortControllers: Map<string, AbortController>
) {
  const chatRequest: ChatRequest = await c.req.json();
  const { debugMode } = c.var.config;
  
  if (debugMode) {
    console.debug(
      "[Multi-Agent] Received chat request:",
      JSON.stringify(chatRequest, null, 2)
    );
  }
  
  const stream = new ReadableStream({
    async start(controller) {
      try {
        // Send connection acknowledgment
        const ackResponse: StreamResponse = {
          type: "claude_json",
          data: {
            type: "system",
            subtype: "connection_ack",
            timestamp: Date.now(),
          }
        };
        controller.enqueue(new TextEncoder().encode(JSON.stringify(ackResponse) + "\n"));
        
        // Process multi-agent request
        for await (const chunk of executeMultiAgentChat(
          chatRequest,
          requestAbortControllers,
          debugMode
        )) {
          const data = JSON.stringify(chunk) + "\n";
          controller.enqueue(new TextEncoder().encode(data));
        }
        
        controller.close();
      } catch (error) {
        const errorResponse: StreamResponse = {
          type: "error",
          error: error instanceof Error ? error.message : String(error),
        };
        controller.enqueue(
          new TextEncoder().encode(JSON.stringify(errorResponse) + "\n")
        );
        controller.close();
      }
    },
  });
  
  return new Response(stream, {
    headers: {
      "Content-Type": "application/x-ndjson",
      "Cache-Control": "no-cache, no-store, must-revalidate",
      "Connection": "keep-alive",
      "Transfer-Encoding": "chunked",
      "X-Accel-Buffering": "no",
      "Access-Control-Allow-Origin": "*",
      "Access-Control-Allow-Headers": "Content-Type",
      "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
    },
  });
}