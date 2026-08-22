import { User, Bot } from "lucide-react";
import type { ChatMessage } from "../../types";
import { useAgentConfig } from "../../hooks/useAgentConfig";

interface MessageBubbleProps {
  message: ChatMessage;
  isLast?: boolean;
}

const getAgentColor = (agentId: string) => {
  // Map agent IDs to CSS color variables, with fallback
  const colorMap: Record<string, string> = {
    "readymojo-admin": "var(--agent-admin)",
    "readymojo-api": "var(--agent-api)", 
    "readymojo-web": "var(--agent-web)",
    "peakmojo-kit": "var(--agent-kit)",
  };
  return colorMap[agentId] || "var(--claude-text-accent)";
};

export function MessageBubble({ message, isLast = false }: MessageBubbleProps) {
  const isUser = message.role === "user";
  const { getAgentById } = useAgentConfig();
  const agent = message.agentId ? getAgentById(message.agentId) : null;
  
  const formatTime = (timestamp: number) => {
    return new Date(timestamp).toLocaleTimeString('en-US', { 
      hour12: false, 
      hour: '2-digit', 
      minute: '2-digit' 
    });
  };

  return (
    <div className={`message-item animate-in ${isLast ? 'last' : ''}`}>
      {/* Avatar */}
      <div 
        className={`message-avatar ${isUser ? 'user' : ''}`}
        style={!isUser && agent ? { 
          backgroundColor: getAgentColor(agent.id)
        } : {}}
      >
        {isUser ? <User size={14} /> : <Bot size={14} />}
      </div>

      {/* Message Content */}
      <div className="message-content">
        {/* Header */}
        <div className="message-header">
          <span className="message-author">
            {isUser ? "You" : agent?.name || "Claude"}
          </span>
          <span className="message-time">
            {formatTime(message.timestamp)}
          </span>
          {agent && (
            <span className="message-agent">
              @{agent.id}
            </span>
          )}
        </div>

        {/* Message Body */}
        <div className="message-body">
          <div className="message-text">
            {message.content}
          </div>
        </div>
      </div>
    </div>
  );
}