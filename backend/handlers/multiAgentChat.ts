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
async function* executeSingleAgent(
  agentId: string,
  request: ChatRequest,
  command: AgentCommand | null,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: string[] = [agentId],
): AsyncGenerator<StreamResponse> {
  const provider = globalRegistry.getProviderForAgent(agentId);
  const agentConfig = globalRegistry.getAgent(agentId);
  
  if (!provider || !agentConfig) {
    yield {
      type: "error",
      error: `Agent '${agentId}' not found or provider not available`,
    };
    return;
  }
  
  // Handle special commands
  if (command?.command === "capture_screen") {
    yield* handleScreenCapture(agentId, request, command, abortController, debugMode);
    return;
  }
  
  // Build provider request
  const providerRequest: ProviderChatRequest = {
    message: request.message,
    sessionId: request.sessionId,
    requestId: request.requestId,
    workingDirectory: request.workingDirectory || agentConfig.workingDirectory,
  };
  
  // Execute with provider
  for await (const response of provider.executeChat(providerRequest, {
    debugMode,
    abortController,
    temperature: agentConfig.config?.temperature,
    maxTokens: agentConfig.config?.maxTokens,
  })) {
    // Convert provider response to stream response
    const chatRoomMessage = createChatRoomMessage(response, agentId);
    
    if (chatRoomMessage) {
      // Send as chat room protocol message
      yield {
        type: "claude_json",
        data: {
          type: "chat_room_message",
          message: chatRoomMessage,
          session_id: request.sessionId,
        },
      };
    }
    
    if (response.type === "tool_use") {
      const toolUseId = response.toolUseId || `${request.requestId}-${agentId}-delegation`;

      yield {
        type: "claude_json",
        data: {
          type: "assistant",
          content: [{
            type: "tool_use",
            id: toolUseId,
            name: response.toolName,
            input: response.toolInput,
          }],
          model: response.metadata?.model,
        },
      };

      if (response.toolName === "delegate_task") {
        const result = await delegateTask(
          response.toolInput,
          toolUseId,
          request,
          abortController,
          debugMode,
          delegationPath,
        );

        if (result.circular) {
          yield { type: "error", error: result.error };
          return;
        }

        const toolResult = JSON.stringify({
          type: "tool_result",
          is_error: result.isError,
          content: result.content,
          tool_use_id: toolUseId,
        });

        yield {
          type: "claude_json",
          data: {
            type: "tool_result",
            content: toolResult,
            tool_use_id: toolUseId,
          },
        };

        // The provider receives the result as the next turn in this session.
        yield* executeSingleAgent(
          agentId,
          { ...request, message: toolResult },
          null,
          abortController,
          debugMode,
          delegationPath,
        );
        return;
      }
    }

    // Also send original response format for compatibility
    if (response.type === "text") {
      yield {
        type: "claude_json",
        data: {
          type: "assistant",
          content: response.content,
          model: response.metadata?.model,
        },
      };
    } else if (response.type === "done") {
      yield { type: "done" };
      return;
    } else if (response.type === "error") {
      yield { type: "error", error: response.error };
      return;
    }
  }
}

type DelegationResult = {
  content: string;
  isError: boolean;
  circular?: boolean;
  error?: string;
};

/**
 * Run a delegated task and collect only its textual output for the parent.
 */
async function delegateTask(
  input: unknown,
  toolUseId: string,
  request: ChatRequest,
  abortController: AbortController,
  debugMode: boolean,
  delegationPath: string[],
): Promise<DelegationResult> {
  const task = input && typeof input === "object" ? input as Record<string, unknown> : {};
  const delegatedAgentId = typeof task.agent_id === "string" ? task.agent_id : "";
  const instructions = typeof task.instructions === "string" ? task.instructions : "";

  if (delegationPath.includes(delegatedAgentId)) {
    return {
      content: `Circular delegation detected for agent '${delegatedAgentId}'.`,
      isError: true,
      circular: true,
      error: `Circular delegation detected: ${delegationPath.join(" -> ")} -> ${delegatedAgentId}`,
    };
  }

  const provider = globalRegistry.getProviderForAgent(delegatedAgentId);
  const agentConfig = globalRegistry.getAgent(delegatedAgentId);
  if (!provider || !agentConfig) {
    const error = `Agent '${delegatedAgentId}' not found or provider not available`;
    return {
      content: error,
      isError: true,
    };
  }

  if (!instructions) {
    return {
      content: "Delegated task did not include instructions.",
      isError: true,
    };
  }

  let output = "";
  let failed = false;
  for await (const chunk of executeSingleAgent(
    delegatedAgentId,
    {
      ...request,
      message: instructions,
      sessionId: undefined,
      requestId: `${request.requestId}:delegate:${toolUseId}`,
    },
    null,
    abortController,
    debugMode,
    [...delegationPath, delegatedAgentId],
  )) {
    if (chunk.type === "error") {
      if (chunk.error?.toLowerCase().includes("circular")) {
        return {
          content: chunk.error,
          isError: true,
          circular: true,
          error: chunk.error,
        };
      }
      failed = true;
      output = chunk.error || "Sub-agent failed without an error message.";
    } else if (chunk.type === "claude_json") {
      const data = chunk.data as { type?: string; content?: unknown } | undefined;
      if (data?.type === "assistant" && typeof data.content === "string") {
        output += data.content;
      }
    }
  }

  if (failed) {
    return { content: output, isError: true };
  }

  return {
    content: output || "Sub-agent completed without producing textual output.",
    isError: false,
  };
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