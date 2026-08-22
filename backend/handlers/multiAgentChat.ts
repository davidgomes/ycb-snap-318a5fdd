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
interface AgentExecutionResult {
  accumulatedText: string;
  error?: string;
  circular?: boolean;
}

interface DelegationState {
  nextGeneratedToolUseId: number;
}

interface AgentExecutionOptions {
  emitErrors: boolean;
  emitDone: boolean;
}

function createSuccessfulResult(accumulatedText = ""): AgentExecutionResult {
  return { accumulatedText };
}

function createFailedResult(
  error: string,
  accumulatedText = "",
): AgentExecutionResult {
  return { accumulatedText, error };
}

function parseDelegationInput(
  input: unknown,
): { agentId: string; instructions: string } | { error: string } {
  if (!input || typeof input !== "object") {
    return { error: "delegate_task input must be an object" };
  }

  const delegationInput = input as Record<string, unknown>;
  const agentId = delegationInput.agent_id;
  const instructions = delegationInput.instructions;

  if (typeof agentId !== "string" || !agentId.trim()) {
    return { error: "delegate_task input must include a non-empty agent_id" };
  }

  if (typeof instructions !== "string" || !instructions.trim()) {
    return {
      error: `delegate_task for agent '${agentId}' must include non-empty instructions`,
    };
  }

  return {
    agentId,
    instructions,
  };
}

function createToolUseStreamResponse(
  response: ProviderResponse,
  toolUseId: string,
  sessionId?: string,
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
            input: response.toolInput || {},
          },
        ],
      },
      session_id: sessionId,
    },
  };
}

function createToolResult(
  isError: boolean,
  content: string,
  toolUseId: string,
): string {
  return JSON.stringify({
    type: "tool_result",
    is_error: isError,
    content,
    tool_use_id: toolUseId,
  });
}

async function* executeAgentConversation(
  agentId: string,
  request: ChatRequest,
  providerRequest: ProviderChatRequest,
  agentConfig: NonNullable<ReturnType<typeof globalRegistry.getAgent>>,
  provider: NonNullable<ReturnType<typeof globalRegistry.getProviderForAgent>>,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: string[],
  state: DelegationState,
  options: AgentExecutionOptions,
): AsyncGenerator<StreamResponse, AgentExecutionResult> {
  let accumulatedText = "";

  try {
    for await (const response of provider.executeChat(providerRequest, {
      debugMode,
      abortController,
      temperature: agentConfig.config?.temperature,
      maxTokens: agentConfig.config?.maxTokens,
    })) {
      if (response.type === "text") {
        const text = response.content || "";
        accumulatedText += text;

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

        yield {
          type: "claude_json",
          data: {
            type: "assistant",
            content: response.content,
            model: response.metadata?.model,
          },
        };
        continue;
      }

      if (response.type === "tool_use") {
        const toolUseId =
          response.toolUseId ||
          `${request.requestId}-${agentId}-tool-${++state.nextGeneratedToolUseId}`;

        yield createToolUseStreamResponse(response, toolUseId, request.sessionId);

        if (response.toolName !== "delegate_task") {
          continue;
        }

        const parsedInput = parseDelegationInput(response.toolInput);
        if ("error" in parsedInput) {
          const toolResult = createToolResult(
            true,
            parsedInput.error,
            toolUseId,
          );
          const continuation = yield* executeAgentConversation(
            agentId,
            request,
            { ...providerRequest, message: toolResult },
            agentConfig,
            provider,
            abortController,
            debugMode,
            delegationPath,
            state,
            options,
          );
          accumulatedText += continuation.accumulatedText;
          if (continuation.error || continuation.circular) {
            return {
              accumulatedText,
              error: continuation.error,
              circular: continuation.circular,
            };
          }
          return createSuccessfulResult(accumulatedText);
        }

        const { agentId: delegatedAgentId, instructions } = parsedInput;
        const delegatedResult = yield* executeDelegatedAgent(
          delegatedAgentId,
          instructions,
          request,
          abortController,
          debugMode,
          [...delegationPath, delegatedAgentId],
          state,
        );

        if (delegatedResult.circular) {
          return {
            accumulatedText,
            error: delegatedResult.error,
            circular: true,
          };
        }

        const delegatedContent = delegatedResult.error
          ? `Sub-agent '${delegatedAgentId}' failed: ${delegatedResult.error}`
          : delegatedResult.accumulatedText || "(Sub-agent produced no textual output.)";
        const toolResult = createToolResult(
          Boolean(delegatedResult.error),
          delegatedContent,
          toolUseId,
        );

        const continuation = yield* executeAgentConversation(
          agentId,
          request,
          { ...providerRequest, message: toolResult },
          agentConfig,
          provider,
          abortController,
          debugMode,
          delegationPath,
          state,
          options,
        );
        accumulatedText += continuation.accumulatedText;

        if (continuation.error || continuation.circular) {
          return {
            accumulatedText,
            error: continuation.error,
            circular: continuation.circular,
          };
        }
        return createSuccessfulResult(accumulatedText);
      }

      if (response.type === "error") {
        if (options.emitErrors) {
          yield { type: "error", error: response.error };
        }
        return createFailedResult(response.error || "Provider execution failed", accumulatedText);
      }

      if (response.type === "done") {
        if (options.emitDone) {
          yield { type: "done" };
        }
        return createSuccessfulResult(accumulatedText);
      }
    }

    if (options.emitDone) {
      yield { type: "done" };
    }
    return createSuccessfulResult(accumulatedText);
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : String(error);
    if (options.emitErrors) {
      yield { type: "error", error: errorMessage };
    }
    return createFailedResult(errorMessage, accumulatedText);
  }
}

async function* executeDelegatedAgent(
  agentId: string,
  instructions: string,
  parentRequest: ChatRequest,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: string[],
  state: DelegationState,
): AsyncGenerator<StreamResponse, AgentExecutionResult> {
  if (delegationPath.slice(0, -1).includes(agentId)) {
    const error = `Detected circular delegation involving agent '${agentId}'`;
    yield { type: "error", error };
    return {
      accumulatedText: "",
      error,
      circular: true,
    };
  }

  const provider = globalRegistry.getProviderForAgent(agentId);
  const agentConfig = globalRegistry.getAgent(agentId);
  if (!provider || !agentConfig) {
    const error = `Agent '${agentId}' not found or provider not available`;
    yield { type: "error", error };
    return createFailedResult(error);
  }

  const delegatedRequest: ChatRequest = {
    ...parentRequest,
    message: instructions,
    sessionId: undefined,
    workingDirectory: agentConfig.workingDirectory,
  };
  const providerRequest: ProviderChatRequest = {
    message: instructions,
    requestId: parentRequest.requestId,
    workingDirectory: agentConfig.workingDirectory,
  };

  return yield* executeAgentConversation(
    agentId,
    delegatedRequest,
    providerRequest,
    agentConfig,
    provider,
    abortController,
    debugMode,
    delegationPath,
    state,
    {
      emitErrors: false,
      emitDone: false,
    },
  );
}

async function* executeSingleAgent(
  agentId: string,
  request: ChatRequest,
  command: AgentCommand | null,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: string[] = [agentId],
  state: DelegationState = { nextGeneratedToolUseId: 0 },
  options: AgentExecutionOptions = { emitErrors: true, emitDone: true },
): AsyncGenerator<StreamResponse, AgentExecutionResult> {
  const provider = globalRegistry.getProviderForAgent(agentId);
  const agentConfig = globalRegistry.getAgent(agentId);
  
  if (!provider || !agentConfig) {
    const error = `Agent '${agentId}' not found or provider not available`;
    if (options.emitErrors) {
      yield { type: "error", error };
    }
    return createFailedResult(error);
  }
  
  // Handle special commands
  if (command?.command === "capture_screen") {
    yield* handleScreenCapture(agentId, request, command, abortController, debugMode);
    return createSuccessfulResult();
  }
  
  // Build provider request
  const providerRequest: ProviderChatRequest = {
    message: request.message,
    sessionId: request.sessionId,
    requestId: request.requestId,
    workingDirectory: request.workingDirectory || agentConfig.workingDirectory,
  };
  
  return yield* executeAgentConversation(
    agentId,
    request,
    providerRequest,
    agentConfig,
    provider,
    abortController,
    debugMode,
    delegationPath,
    state,
    options,
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