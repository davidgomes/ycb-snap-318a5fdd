import SwiftUI

struct MessageBubbleView: View {
    let message: ChatMessage
    let showAgentName: Bool
    
    init(message: ChatMessage, showAgentName: Bool = true) {
        self.message = message
        self.showAgentName = showAgentName
    }
    
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            if message.isUserMessage {
                Spacer()
                messageContent
                    .frame(maxWidth: UIScreen.main.bounds.width * 0.75, alignment: .trailing)
            } else {
                avatarView
                messageContent
                    .frame(maxWidth: UIScreen.main.bounds.width * 0.75, alignment: .leading)
                Spacer()
            }
        }
        .padding(.horizontal)
    }
    
    @ViewBuilder
    private var avatarView: some View {
        Circle()
            .fill(avatarBackgroundColor)
            .frame(width: 32, height: 32)
            .overlay(
                Image(systemName: avatarIcon)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundColor(avatarIconColor)
            )
    }
    
    @ViewBuilder
    private var messageContent: some View {
        VStack(alignment: messageAlignment, spacing: 6) {
            // Agent name header (for non-user messages in group chat)
            if showAgentName && !message.isUserMessage {
                HStack {
                    Text(agentDisplayName)
                        .font(.caption)
                        .fontWeight(.medium)
                        .foregroundColor(agentNameColor)
                    Spacer()
                    Text(message.timestamp.timeAgoDisplay())
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }
            
            // Message content based on type
            messageBody
            
            // Timestamp for user messages
            if message.isUserMessage {
                HStack {
                    Spacer()
                    Text(message.timestamp.timeAgoDisplay())
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }
        }
        .padding(.vertical, 10)
        .padding(.horizontal, 14)
        .background(bubbleBackgroundColor)
        .foregroundColor(bubbleTextColor)
        .cornerRadius(
            Constants.Dimensions.cornerRadius,
            corners: bubbleCorners
        )
    }
    
    @ViewBuilder
    private var messageBody: some View {
        switch message.type {
        case .chat, .assistant:
            Text(message.content)
                .font(.body)
                .multilineTextAlignment(messageAlignment == .trailing ? .trailing : .leading)
            
        case .system:
            HStack(spacing: 6) {
                Image(systemName: "info.circle.fill")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Text(message.content)
                    .font(.caption)
                    .fontWeight(.medium)
            }
            
        case .tool:
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Image(systemName: Constants.Icons.tool)
                        .foregroundColor(Constants.Colors.primary)
                    Text(message.toolName ?? "Tool")
                        .fontWeight(.medium)
                    Spacer()
                }
                
                if !message.content.isEmpty {
                    Text(message.content)
                        .font(.body)
                }
                
                if let toolInput = message.toolInput, !toolInput.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(Array(toolInput.keys.sorted()), id: \.self) { key in
                            HStack {
                                Text("\(key):")
                                    .fontWeight(.medium)
                                Text(toolInput[key] ?? "")
                                    .foregroundColor(.secondary)
                            }
                            .font(.caption)
                        }
                    }
                    .padding(.top, 4)
                }
            }
            
        case .toolResult:
            HStack(spacing: 6) {
                Image(systemName: Constants.Icons.success)
                    .foregroundColor(Constants.Colors.success)
                Text(message.displayContent)
                    .font(.body)
            }
            
        case .error:
            HStack(spacing: 6) {
                Image(systemName: Constants.Icons.error)
                    .foregroundColor(Constants.Colors.error)
                Text(message.content)
                    .font(.body)
            }
            
        case .executionPlan:
            ExecutionPlanView(steps: message.executionSteps ?? [])
        }
    }
    
    // MARK: - Computed Properties
    private var messageAlignment: HorizontalAlignment {
        return message.isUserMessage ? .trailing : .leading
    }
    
    private var bubbleCorners: UIRectCorner {
        if message.isUserMessage {
            return [.topLeft, .topRight, .bottomLeft]
        } else {
            return [.topLeft, .topRight, .bottomRight]
        }
    }
    
    private var bubbleBackgroundColor: Color {
        switch message.type {
        case .chat where message.isUserMessage:
            return Constants.Colors.primary
        case .assistant, .chat:
            return Color.messageBackground
        case .system:
            return Constants.Colors.success.opacity(0.1)
        case .tool:
            return Constants.Colors.primary.opacity(0.1)
        case .toolResult:
            return Constants.Colors.success.opacity(0.1)
        case .error:
            return Constants.Colors.error.opacity(0.1)
        case .executionPlan:
            return Constants.Colors.accent.opacity(0.1)
        }
    }
    
    private var bubbleTextColor: Color {
        if message.isUserMessage && message.type == .chat {
            return .white
        } else {
            return .primary
        }
    }
    
    private var avatarBackgroundColor: Color {
        switch message.type {
        case .system:
            return Constants.Colors.success.opacity(0.2)
        case .tool, .toolResult:
            return Constants.Colors.primary.opacity(0.2)
        case .error:
            return Constants.Colors.error.opacity(0.2)
        case .executionPlan:
            return Constants.Colors.accent.opacity(0.2)
        default:
            return Constants.Colors.secondary.opacity(0.2)
        }
    }
    
    private var avatarIcon: String {
        switch message.type {
        case .system:
            return "gear"
        case .tool, .toolResult:
            return Constants.Icons.tool
        case .error:
            return Constants.Icons.error
        case .executionPlan:
            return "list.bullet"
        default:
            return Constants.Icons.agent
        }
    }
    
    private var avatarIconColor: Color {
        switch message.type {
        case .system:
            return Constants.Colors.success
        case .tool, .toolResult:
            return Constants.Colors.primary
        case .error:
            return Constants.Colors.error
        case .executionPlan:
            return Constants.Colors.accent
        default:
            return Constants.Colors.secondary
        }
    }
    
    private var agentDisplayName: String {
        return message.agentId ?? "Assistant"
    }
    
    private var agentNameColor: Color {
        return Constants.Colors.primary
    }
}

#Preview {
    ScrollView {
        VStack(spacing: 16) {
            // User message
            MessageBubbleView(
                message: ChatMessage(
                    type: .chat,
                    role: .user,
                    content: "Create a user authentication system with login and registration",
                    agentId: "user"
                ),
                showAgentName: false
            )
            
            // Assistant message
            MessageBubbleView(
                message: ChatMessage(
                    type: .assistant,
                    role: .assistant,
                    content: "I'll help you create a user authentication system. Let me break this down into steps and coordinate with our team of agents.",
                    agentId: "orchestrator-agent"
                )
            )
            
            // Tool message
            MessageBubbleView(
                message: ChatMessage(
                    type: .tool,
                    role: .assistant,
                    content: "Creating user model and endpoints",
                    agentId: "api-agent",
                    toolName: "CreateFile",
                    toolInput: ["filename": "user.model.js", "content": "User model code..."]
                )
            )
            
            // Execution plan
            MessageBubbleView(
                message: ChatMessage(
                    type: .executionPlan,
                    role: .assistant,
                    content: "Execution plan created",
                    agentId: "orchestrator-agent",
                    executionSteps: ExecutionStep.sampleSteps
                )
            )
            
            // Error message
            MessageBubbleView(
                message: ChatMessage(
                    type: .error,
                    role: .system,
                    content: "Failed to connect to API endpoint",
                    agentId: "api-agent"
                )
            )
        }
        .padding()
    }
    .background(Color.chatBackground)
}

extension ExecutionStep {
    static let sampleSteps = [
        ExecutionStep(id: "1", agent: "api-agent", message: "Create user model and authentication endpoints"),
        ExecutionStep(id: "2", agent: "frontend-agent", message: "Create login and registration forms", dependencies: ["1"])
    ]
}