import { PREDEFINED_AGENTS } from "../../config/agents";

interface AgentSelectorProps {
  activeAgentId: string | null;
  onAgentSelect: (agentId: string) => void;
  agentSessions: Record<string, { sessionId: string | null; messages: any[] }>;
}

export function AgentSelector({ activeAgentId, onAgentSelect, agentSessions }: AgentSelectorProps) {
  return (
    <div className="border-b border-border bg-card p-4">
      <div className="mb-2">
        <h3 className="text-sm font-semibold text-card-foreground">Agents</h3>
        <p className="text-xs text-muted-foreground">Click an agent to switch, or use @agent-name in your message</p>
      </div>
      <div className="flex flex-wrap gap-2">
        {PREDEFINED_AGENTS.map((agent) => {
          const isActive = activeAgentId === agent.id;
          const hasMessages = agentSessions[agent.id]?.messages.length > 0;
          const messageCount = agentSessions[agent.id]?.messages.length || 0;
          
          return (
            <button
              key={agent.id}
              onClick={() => onAgentSelect(agent.id)}
              className={`
                flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-all
                ${isActive 
                  ? `${agent.color} text-white shadow-md` 
                  : "bg-muted text-muted-foreground hover:bg-muted/80"
                }
              `}
            >
              <div className={`w-2 h-2 rounded-full ${isActive ? 'bg-white' : agent.color}`}></div>
              
              <span>{agent.name}</span>
              
              {hasMessages && (
                <span className={`
                  text-xs px-1.5 py-0.5 rounded-full
                  ${isActive 
                    ? 'bg-white/20 text-white' 
                    : 'bg-primary/10 text-primary'
                  }
                `}>
                  {messageCount}
                </span>
              )}
            </button>
          );
        })}
      </div>
      
      {activeAgentId && (
        <div className="mt-3 text-xs text-muted-foreground">
          <span className="font-medium">Active:</span> {PREDEFINED_AGENTS.find(a => a.id === activeAgentId)?.description}
        </div>
      )}
    </div>
  );
}