import Anthropic from "@anthropic-ai/sdk";
import type { ChatRequest, StreamResponse } from "../../shared/types.ts";
import { globalRegistry } from "../providers/registry.ts";
import type { ProviderChatRequest } from "../providers/types.ts";

const DELEGATE_TASK_TOOL_NAME = "delegate_task";
const NO_OUTPUT_PLACEHOLDER = "Sub-agent completed with no textual output.";
const MAX_DELEGATION_ITERATIONS = 10;

export interface DelegationToolResultPayload {
  type: "tool_result";
  is_error: boolean;
  content: string;
  tool_use_id: string;
}

export interface DelegateTaskInput {
  agent_id: string;
  instructions: string;
}

type WorkerAgent = {
  id: string;
  name: string;
  description: string;
};

function getWorkerAgents(request: ChatRequest): WorkerAgent[] {
  if (request.availableAgents && request.availableAgents.length > 0) {
    return request.availableAgents
      .filter((agent) => !agent.isOrchestrator)
      .map((agent) => ({
        id: agent.id,
        name: agent.name,
        description: agent.description,
      }));
  }

  return globalRegistry
    .getAllAgents()
    .filter((agent) => !agent.isOrchestrator)
    .map((agent) => ({
      id: agent.id,
      name: agent.name,
      description: agent.description,
    }));
}

export function createDelegateTaskTool(workerAgents: WorkerAgent[]): Anthropic.Tool {
  const agentIds = workerAgents.map((agent) => agent.id);

  return {
    name: DELEGATE_TASK_TOOL_NAME,
    description:
      "Delegate a task to another specialized agent. The sub-agent will execute the instructions and return its output.",
    input_schema: {
      type: "object",
      properties: {
        agent_id: {
          type: "string",
          description: "ID of the agent to delegate the task to",
          ...(agentIds.length > 0 ? { enum: agentIds } : {}),
        },
        instructions: {
          type: "string",
          description: "Clear instructions for the sub-agent to execute",
        },
      },
      required: ["agent_id", "instructions"],
    },
  };
}

export function buildDelegationToolResultContent(
  toolUseId: string,
  content: string,
  isError: boolean,
): string {
  const payload: DelegationToolResultPayload = {
    type: "tool_result",
    is_error: isError,
    content,
    tool_use_id: toolUseId,
  };
  return JSON.stringify(payload);
}

export function isAgentKnown(
  agentId: string,
  request: ChatRequest,
): boolean {
  if (globalRegistry.getAgent(agentId)) {
    return true;
  }

  return !!request.availableAgents?.some((agent) => agent.id === agentId);
}

export function isCircularDelegation(
  delegationChain: string[],
  delegatingAgentId: string,
  targetAgentId: string,
): boolean {
  if (targetAgentId === delegatingAgentId) {
    return true;
  }

  return delegationChain.includes(targetAgentId);
}

async function collectSubAgentOutput(
  targetAgentId: string,
  instructions: string,
  request: ChatRequest,
  abortController: AbortController,
  debugMode: boolean,
): Promise<{ output: string; isError: boolean }> {
  const provider = globalRegistry.getProviderForAgent(targetAgentId);
  const agentConfig = globalRegistry.getAgent(targetAgentId);

  if (!provider || !agentConfig) {
    return {
      output: `Sub-agent '${targetAgentId}' failed: agent not found or provider unavailable`,
      isError: true,
    };
  }

  const providerRequest: ProviderChatRequest = {
    message: instructions,
    sessionId: request.sessionId,
    requestId: `${request.requestId}-delegate-${targetAgentId}-${Date.now()}`,
    workingDirectory: request.workingDirectory || agentConfig.workingDirectory,
  };

  let accumulatedText = "";
  let sawError = false;
  let errorMessage = "";

  try {
    for await (const response of provider.executeChat(providerRequest, {
      debugMode,
      abortController,
      temperature: agentConfig.config?.temperature,
      maxTokens: agentConfig.config?.maxTokens,
    })) {
      if (response.type === "text" && response.content) {
        accumulatedText += response.content;
      } else if (response.type === "error") {
        sawError = true;
        errorMessage = response.error || "Sub-agent execution failed";
        break;
      } else if (response.type === "done") {
        break;
      }
    }
  } catch (error) {
    sawError = true;
    errorMessage = error instanceof Error ? error.message : String(error);
  }

  if (sawError) {
    return { output: errorMessage, isError: true };
  }

  if (!accumulatedText.trim()) {
    return { output: NO_OUTPUT_PLACEHOLDER, isError: false };
  }

  return { output: accumulatedText, isError: false };
}

function buildSystemPrompt(workerAgents: WorkerAgent[]): string {
  const agentDescriptions = workerAgents
    .map((agent) => `- ${agent.id}: ${agent.description}`)
    .join("\n");

  return `You are a coordinating agent in a multi-agent workspace. Use the ${DELEGATE_TASK_TOOL_NAME} tool to delegate specialized work to other agents, then synthesize their results into a helpful response for the user.

Available worker agents:
${agentDescriptions || "- No worker agents configured"}

When delegating, provide clear, self-contained instructions for the sub-agent. After receiving delegation results, continue the conversation and incorporate the sub-agent output.`;
}

function yieldAssistantMessage(
  message: Anthropic.Message,
  sessionId: string,
): StreamResponse {
  return {
    type: "claude_json",
    data: {
      type: "assistant",
      message,
      session_id: sessionId,
    },
  };
}

function yieldToolResultUserMessage(
  toolUseId: string,
  resultContent: string,
  isError: boolean,
  sessionId: string,
): StreamResponse {
  return {
    type: "claude_json",
    data: {
      type: "user",
      message: {
        role: "user",
        content: [
          {
            type: "tool_result",
            tool_use_id: toolUseId,
            content: resultContent,
            is_error: isError,
          },
        ],
      },
      session_id: sessionId,
    },
  };
}

async function* streamAnthropicMessage(
  stream: AsyncIterable<Anthropic.MessageStreamEvent>,
  sessionId: string,
  debugMode: boolean,
): AsyncGenerator<
  StreamResponse,
  { message: Anthropic.Message; toolUses: Anthropic.ToolUseBlock[] }
> {
  let currentMessage: Anthropic.Message | null = null;
  const currentContent: Anthropic.ContentBlock[] = [];

  for await (const chunk of stream) {
    if (debugMode) {
      console.debug("[Delegation] Anthropic chunk:", chunk.type);
    }

    if (chunk.type === "message_start") {
      currentMessage = {
        ...chunk.message,
        content: [],
        stop_reason: null,
        stop_sequence: null,
      };
    } else if (chunk.type === "content_block_start") {
      const contentBlock = { ...chunk.content_block };
      if (contentBlock.type === "tool_use") {
        contentBlock.input = "";
      }
      currentContent.push(contentBlock);
    } else if (chunk.type === "content_block_delta") {
      const lastContent = currentContent[currentContent.length - 1];
      if (!lastContent) continue;

      if (chunk.delta.type === "text_delta" && lastContent.type === "text") {
        lastContent.text = (lastContent.text || "") + chunk.delta.text;
      } else if (
        chunk.delta.type === "input_json_delta" &&
        lastContent.type === "tool_use"
      ) {
        if (typeof lastContent.input !== "string") {
          lastContent.input = "";
        }
        lastContent.input += chunk.delta.partial_json;
      }
    } else if (chunk.type === "content_block_stop") {
      const lastContent = currentContent[currentContent.length - 1];
      if (lastContent?.type === "tool_use" && typeof lastContent.input === "string") {
        try {
          lastContent.input = lastContent.input.trim()
            ? JSON.parse(lastContent.input)
            : {};
        } catch {
          lastContent.input = {};
        }
      }
    } else if (chunk.type === "message_delta" && currentMessage) {
      currentMessage.stop_reason = chunk.delta.stop_reason;
      currentMessage.stop_sequence = chunk.delta.stop_sequence;
      if (chunk.usage) {
        currentMessage.usage = {
          ...currentMessage.usage,
          ...chunk.usage,
        } as Anthropic.Message["usage"];
      }
    } else if (chunk.type === "message_stop" && currentMessage) {
      currentMessage.content = currentContent;
      yield yieldAssistantMessage(currentMessage, sessionId);

      const toolUses = currentContent.filter(
        (block): block is Anthropic.ToolUseBlock => block.type === "tool_use",
      );

      return { message: currentMessage, toolUses };
    }
  }

  throw new Error("Anthropic stream ended without a completed message");
}

async function* handleDelegateTask(
  toolUse: Anthropic.ToolUseBlock,
  delegatingAgentId: string,
  delegationChain: string[],
  request: ChatRequest,
  abortController: AbortController,
  debugMode: boolean,
  sessionId: string,
): AsyncGenerator<StreamResponse, { content: string; isError: boolean }> {
  const input = (toolUse.input || {}) as Partial<DelegateTaskInput>;
  const targetAgentId = input.agent_id || "unknown";
  const instructions = input.instructions || "";

  if (
    isCircularDelegation(delegationChain, delegatingAgentId, targetAgentId)
  ) {
    const errorMessage = `Circular delegation detected: agent '${delegatingAgentId}' cannot delegate to '${targetAgentId}' because it would create a circular dependency`;
    yield {
      type: "error",
      error: errorMessage,
    };

    const resultContent = buildDelegationToolResultContent(
      toolUse.id,
      errorMessage,
      true,
    );
    yield yieldToolResultUserMessage(toolUse.id, resultContent, true, sessionId);
    return { content: resultContent, isError: true };
  }

  if (!isAgentKnown(targetAgentId, request)) {
    const errorMessage = `Unknown agent '${targetAgentId}' - agent is not registered or available`;
    yield {
      type: "error",
      error: errorMessage,
    };

    const resultContent = buildDelegationToolResultContent(
      toolUse.id,
      `Unknown agent: ${targetAgentId}`,
      true,
    );
    yield yieldToolResultUserMessage(toolUse.id, resultContent, true, sessionId);
    return { content: resultContent, isError: true };
  }

  const orchestratorConfig = globalRegistry.getAgent(targetAgentId);
  const isDelegatingSubAgent =
    orchestratorConfig?.isOrchestrator ||
    targetAgentId === "orchestrator";

  let subAgentOutput: string;
  let subAgentIsError: boolean;

  if (isDelegatingSubAgent) {
    let nestedOutput = "";

    for await (const chunk of executeWithDelegation(
      targetAgentId,
      {
        ...request,
        message: instructions,
        requestId: `${request.requestId}-nested-${targetAgentId}-${Date.now()}`,
      },
      abortController,
      debugMode,
      [...delegationChain, delegatingAgentId],
    )) {
      yield chunk;

      if (chunk.type === "claude_json") {
        const data = chunk.data as {
          type?: string;
          message?: { content?: Array<{ type?: string; text?: string }> };
        };
        if (data.type === "assistant" && Array.isArray(data.message?.content)) {
          for (const block of data.message.content) {
            if (block.type === "text" && block.text) {
              nestedOutput += block.text;
            }
          }
        }
      } else if (chunk.type === "error") {
        const errorMsg = chunk.error || "";
        const isDelegationHandlingError =
          errorMsg.includes("Circular delegation") ||
          errorMsg.includes("Unknown agent");

        if (!isDelegationHandlingError) {
          subAgentOutput = errorMsg || "Nested delegation failed";
          subAgentIsError = true;
          const resultContent = buildDelegationToolResultContent(
            toolUse.id,
            subAgentOutput,
            true,
          );
          yield yieldToolResultUserMessage(toolUse.id, resultContent, true, sessionId);
          return { content: resultContent, isError: true };
        }
      }
    }

    subAgentOutput = nestedOutput.trim() || NO_OUTPUT_PLACEHOLDER;
    subAgentIsError = false;
  } else {
    const result = await collectSubAgentOutput(
      targetAgentId,
      instructions,
      request,
      abortController,
      debugMode,
    );
    subAgentOutput = result.output;
    subAgentIsError = result.isError;
  }

  const resultContent = buildDelegationToolResultContent(
    toolUse.id,
    subAgentOutput,
    subAgentIsError,
  );

  yield yieldToolResultUserMessage(
    toolUse.id,
    resultContent,
    subAgentIsError,
    sessionId,
  );

  return { content: resultContent, isError: subAgentIsError };
}

export async function* executeWithDelegation(
  agentId: string,
  request: ChatRequest,
  abortController: AbortController,
  debugMode: boolean = false,
  delegationChain: string[] = [],
): AsyncGenerator<StreamResponse> {
  const apiKey = process.env.ANTHROPIC_API_KEY || process.env.CLAUDE_API_KEY;

  if (!apiKey) {
    yield {
      type: "error",
      error: "ANTHROPIC_API_KEY environment variable is required for agent delegation",
    };
    return;
  }

  const workerAgents = getWorkerAgents(request);
  const tools = [createDelegateTaskTool(workerAgents)];
  const sessionId = request.sessionId || `delegation-${Date.now()}`;
  const anthropic = new Anthropic({ apiKey });

  const messages: Anthropic.MessageParam[] = [
    {
      role: "user",
      content: request.message,
    },
  ];

  yield {
    type: "claude_json",
    data: {
      type: "system",
      subtype: "init",
      session_id: sessionId,
      model: "claude-sonnet-4-20250514",
      tools: [DELEGATE_TASK_TOOL_NAME],
    },
  };

  for (let iteration = 0; iteration < MAX_DELEGATION_ITERATIONS; iteration++) {
    if (abortController.signal.aborted) {
      yield { type: "aborted" };
      return;
    }

    const stream = await anthropic.messages.create({
      model: "claude-sonnet-4-20250514",
      max_tokens: 4000,
      system: buildSystemPrompt(workerAgents),
      messages,
      tools,
      stream: true,
    });

    const { message, toolUses } = yield* streamAnthropicMessage(
      stream,
      sessionId,
      debugMode,
    );

    messages.push({
      role: "assistant",
      content: message.content,
    });

    if (toolUses.length === 0) {
      yield { type: "done" };
      return;
    }

    const toolResultBlocks: Anthropic.ToolResultBlockParam[] = [];

    for (const toolUse of toolUses) {
      if (toolUse.name !== DELEGATE_TASK_TOOL_NAME) {
        const unsupportedContent = buildDelegationToolResultContent(
          toolUse.id,
          `Unsupported tool: ${toolUse.name}`,
          true,
        );
        toolResultBlocks.push({
          type: "tool_result",
          tool_use_id: toolUse.id,
          content: unsupportedContent,
          is_error: true,
        });
        yield yieldToolResultUserMessage(
          toolUse.id,
          unsupportedContent,
          true,
          sessionId,
        );
        continue;
      }

      const result = yield* handleDelegateTask(
        toolUse,
        agentId,
        delegationChain,
        request,
        abortController,
        debugMode,
        sessionId,
      );

      toolResultBlocks.push({
        type: "tool_result",
        tool_use_id: toolUse.id,
        content: result.content,
        is_error: result.isError,
      });
    }

    messages.push({
      role: "user",
      content: toolResultBlocks,
    });
  }

  yield {
    type: "error",
    error: `Maximum delegation iterations (${MAX_DELEGATION_ITERATIONS}) exceeded`,
  };
}
