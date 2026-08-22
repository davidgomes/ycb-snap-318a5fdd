import Foundation
import Combine

enum ChatMode {
    case group
    case individual
}

class AgentHubViewModel: ObservableObject {
    @Published var currentMode: ChatMode = .group
    @Published var activeAgentId: String?
    @Published var agentSessions: [String: AgentSession] = [:]
    @Published var groupChatMessages: [ChatMessage] = []
    @Published var groupSessionId: String?
    @Published var lastUsedAgentId: String?
    
    var appSettings: AppSettings
    
    init(appSettings: AppSettings) {
        self.appSettings = appSettings
        
        // Initialize sessions for all agents
        initializeAgentSessions()
    }
    
    private func initializeAgentSessions() {
        for agent in appSettings.agents {
            if agentSessions[agent.id] == nil {
                agentSessions[agent.id] = AgentSession(agentId: agent.id)
            }
        }
    }
    
    // MARK: - Mode Management
    func switchToGroupMode() {
        currentMode = .group
        activeAgentId = nil
    }
    
    func switchToIndividualMode(agentId: String) {
        currentMode = .individual
        activeAgentId = agentId
        lastUsedAgentId = agentId
        
        // Update last active time for the agent
        agentSessions[agentId]?.lastActiveTime = Date()
    }
    
    // MARK: - Message Management
    func addMessage(_ message: ChatMessage, toGroupChat: Bool = false) {
        if toGroupChat || currentMode == .group {
            groupChatMessages.append(message)
        } else if let agentId = activeAgentId {
            agentSessions[agentId]?.messages.append(message)
        }
    }
    
    func updateLastMessage(content: String, inGroupChat: Bool = false) {
        if inGroupChat || currentMode == .group {
            if var lastMessage = groupChatMessages.last {
                lastMessage.content = content
                groupChatMessages[groupChatMessages.count - 1] = lastMessage
            }
        } else if let agentId = activeAgentId,
                  var session = agentSessions[agentId],
                  !session.messages.isEmpty {
            var lastMessage = session.messages.last!
            lastMessage.content = content
            session.messages[session.messages.count - 1] = lastMessage
            agentSessions[agentId] = session
        }
    }
    
    func getMessages() -> [ChatMessage] {
        if currentMode == .group {
            return groupChatMessages
        } else if let agentId = activeAgentId {
            return agentSessions[agentId]?.messages ?? []
        }
        return []
    }
    
    func getCurrentSessionId() -> String? {
        if currentMode == .group {
            return groupSessionId
        } else if let agentId = activeAgentId {
            return agentSessions[agentId]?.sessionId
        }
        return nil
    }
    
    func setSessionId(_ sessionId: String, forGroupChat: Bool = false) {
        if forGroupChat || currentMode == .group {
            groupSessionId = sessionId
        } else if let agentId = activeAgentId {
            agentSessions[agentId]?.sessionId = sessionId
        }
    }
    
    // MARK: - Agent Management
    func getOrchestratorAgent() -> Agent? {
        return appSettings.getOrchestratorAgent()
    }
    
    func getWorkerAgents() -> [Agent] {
        return appSettings.getWorkerAgents()
    }
    
    func getAgent(withId agentId: String) -> Agent? {
        return appSettings.getAgent(withId: agentId)
    }
    
    func getTargetAgentId() -> String? {
        // If we have a last used agent, return it
        if let lastUsed = lastUsedAgentId,
           appSettings.getAgent(withId: lastUsed) != nil {
            return lastUsed
        }
        
        // Otherwise, return the first available agent
        return appSettings.agents.first(where: { $0.isEnabled })?.id
    }
    
    // MARK: - Session Management
    func clearSession(agentId: String? = nil) {
        if let agentId = agentId {
            agentSessions[agentId] = AgentSession(agentId: agentId)
        } else if currentMode == .group {
            groupChatMessages.removeAll()
            groupSessionId = nil
        } else if let activeId = activeAgentId {
            agentSessions[activeId] = AgentSession(agentId: activeId)
        }
    }
    
    func getActiveAgentSessions() -> [AgentSession] {
        return Array(agentSessions.values)
            .filter { !$0.messages.isEmpty }
            .sorted { $0.lastActiveTime > $1.lastActiveTime }
    }
    
    // MARK: - Agent Mention Parsing
    func parseAgentMention(from message: String) -> (agentId: String, cleanMessage: String)? {
        if let mention = message.extractMention() {
            return (agentId: mention.agentId, cleanMessage: mention.message)
        }
        return nil
    }
    
    // MARK: - Validation
    func canSendMessage(content: String) -> Bool {
        let trimmedContent = content.trimmed()
        return !trimmedContent.isEmpty
    }
    
    func validateAgentSelection() -> Bool {
        if currentMode == .group {
            return getOrchestratorAgent() != nil
        } else {
            return activeAgentId != nil && getAgent(withId: activeAgentId!) != nil
        }
    }
}