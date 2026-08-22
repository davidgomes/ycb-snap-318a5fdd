import Foundation

enum ChatMessageType: String, Codable {
    case chat = "chat"
    case system = "system"
    case assistant = "assistant"
    case tool = "tool"
    case toolResult = "tool_result"
    case error = "error"
    case executionPlan = "execution_plan"
}

enum ChatMessageRole: String, Codable {
    case user = "user"
    case assistant = "assistant"
    case system = "system"
}

struct ChatMessage: Codable, Identifiable, Hashable {
    let id: String
    let type: ChatMessageType
    let role: ChatMessageRole?
    var content: String
    let timestamp: Date
    let agentId: String?
    
    // For tool messages
    let toolName: String?
    let toolInput: [String: String]?
    
    // For execution plan messages
    let executionSteps: [ExecutionStep]?
    
    // For error messages
    let subtype: String?
    
    init(
        id: String = UUID().uuidString,
        type: ChatMessageType,
        role: ChatMessageRole? = nil,
        content: String,
        timestamp: Date = Date(),
        agentId: String? = nil,
        toolName: String? = nil,
        toolInput: [String: String]? = nil,
        executionSteps: [ExecutionStep]? = nil,
        subtype: String? = nil
    ) {
        self.id = id
        self.type = type
        self.role = role
        self.content = content
        self.timestamp = timestamp
        self.agentId = agentId
        self.toolName = toolName
        self.toolInput = toolInput
        self.executionSteps = executionSteps
        self.subtype = subtype
    }
    
    var displayContent: String {
        switch type {
        case .tool:
            return "🔧 \(toolName ?? "Tool") executed"
        case .toolResult:
            return content.isEmpty ? "Tool completed" : content
        case .error:
            return "❌ \(content)"
        case .executionPlan:
            return "📋 Execution Plan (\(executionSteps?.count ?? 0) steps)"
        default:
            return content
        }
    }
    
    var isUserMessage: Bool {
        return type == .chat && role == .user
    }
    
    var isAssistantMessage: Bool {
        return type == .assistant || (type == .chat && role == .assistant)
    }
}

struct ExecutionStep: Codable, Identifiable, Hashable {
    let id: String
    let agent: String
    let message: String
    let dependencies: [String]?
    var status: ExecutionStatus
    
    enum ExecutionStatus: String, Codable {
        case pending = "pending"
        case running = "running"
        case completed = "completed"
        case failed = "failed"
    }
    
    init(id: String, agent: String, message: String, dependencies: [String]? = nil, status: ExecutionStatus = .pending) {
        self.id = id
        self.agent = agent
        self.message = message
        self.dependencies = dependencies
        self.status = status
    }
}

extension ChatMessage {
    static let sampleMessages: [ChatMessage] = [
        ChatMessage(
            type: .chat,
            role: .user,
            content: "Create a user authentication system",
            agentId: "orchestrator-agent"
        ),
        ChatMessage(
            type: .assistant,
            role: .assistant,
            content: "I'll help you create a user authentication system. Let me break this down into steps for our team of agents.",
            agentId: "orchestrator-agent"
        ),
        ChatMessage(
            type: .executionPlan,
            role: .assistant,
            content: "Execution plan created",
            agentId: "orchestrator-agent",
            executionSteps: [
                ExecutionStep(id: "1", agent: "api-agent", message: "Create user model and authentication endpoints"),
                ExecutionStep(id: "2", agent: "frontend-agent", message: "Create login and registration forms", dependencies: ["1"])
            ]
        )
    ]
}