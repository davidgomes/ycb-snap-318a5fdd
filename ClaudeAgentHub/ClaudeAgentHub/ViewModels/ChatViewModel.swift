import Foundation
import Combine

class ChatViewModel: ObservableObject {
    @Published var inputText: String = ""
    @Published var isLoading: Bool = false
    @Published var currentRequestId: String?
    @Published var hasReceivedInit: Bool = false
    @Published var currentAssistantMessage: ChatMessage?
    @Published var errorMessage: String?
    
    private let streamingService = ClaudeStreamingService()
    private let apiService = APIService()
    private var cancellables = Set<AnyCancellable>()
    
    var appSettings: AppSettings
    private var agentHubViewModel: AgentHubViewModel
    
    init(appSettings: AppSettings, agentHubViewModel: AgentHubViewModel) {
        self.appSettings = appSettings
        self.agentHubViewModel = agentHubViewModel
    }
    
    // MARK: - Message Sending
    func sendMessage() {
        guard canSendMessage() else { return }
        
        let messageContent = inputText.trimmed()
        let requestId = UUID().uuidString
        
        // Determine target agent and clean message
        var targetAgentId: String?
        var cleanMessage = messageContent
        
        if agentHubViewModel.currentMode == .group {
            // In group mode, check for @mentions or route to orchestrator
            if let mention = messageContent.extractMention() {
                if let agent = agentHubViewModel.getAgent(withId: mention.agentId) {
                    targetAgentId = agent.id
                    cleanMessage = mention.message
                }
            } else {
                targetAgentId = agentHubViewModel.getOrchestratorAgent()?.id
            }
        } else {
            targetAgentId = agentHubViewModel.activeAgentId
        }
        
        guard let agentId = targetAgentId,
              let targetAgent = agentHubViewModel.getAgent(withId: agentId) else {
            errorMessage = "No valid agent selected"
            return
        }
        
        // Create user message
        let userMessage = ChatMessage(
            type: .chat,
            role: .user,
            content: cleanMessage,
            agentId: agentId
        )
        
        // Add message to appropriate context
        agentHubViewModel.addMessage(userMessage, toGroupChat: agentHubViewModel.currentMode == .group)
        
        // Clear input and start loading
        inputText = ""
        startRequest(requestId: requestId)
        
        // Send to streaming service
        streamingService.sendMessage(
            message: cleanMessage,
            sessionId: agentHubViewModel.getCurrentSessionId(),
            requestId: requestId,
            workingDirectory: targetAgent.workingDirectory,
            availableAgents: appSettings.agents,
            baseURL: appSettings.apiBaseURL,
            onStreamUpdate: { [weak self] response in
                self?.handleStreamResponse(response)
            },
            onCompletion: { [weak self] result in
                self?.handleStreamCompletion(result)
            }
        )
        
        // Trigger haptic feedback
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.light.trigger()
        }
    }
    
    // MARK: - Request Management
    private func startRequest(requestId: String) {
        isLoading = true
        currentRequestId = requestId
        hasReceivedInit = false
        currentAssistantMessage = nil
        errorMessage = nil
    }
    
    func abortCurrentRequest() {
        guard let requestId = currentRequestId else { return }
        
        Task {
            do {
                try await apiService.abortRequest(baseURL: appSettings.apiBaseURL, requestId: requestId)
                
                await MainActor.run {
                    self.resetRequestState()
                    
                    // Add abort message
                    let abortMessage = ChatMessage(
                        type: .system,
                        role: .system,
                        content: "Request aborted by user",
                        agentId: self.agentHubViewModel.activeAgentId
                    )
                    self.agentHubViewModel.addMessage(abortMessage, toGroupChat: self.agentHubViewModel.currentMode == .group)
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = "Failed to abort request: \(error.localizedDescription)"
                }
            }
        }
        
        // Cancel the streaming task
        streamingService.cancelCurrentRequest()
        
        // Trigger haptic feedback
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.warning.trigger()
        }
    }
    
    private func resetRequestState() {
        isLoading = false
        currentRequestId = nil
        hasReceivedInit = false
        currentAssistantMessage = nil
    }
    
    // MARK: - Stream Response Handling
    private func handleStreamResponse(_ response: StreamResponse) {
        // Extract session ID if available
        if let sessionId = streamingService.extractSessionId(from: response) {
            agentHubViewModel.setSessionId(sessionId, forGroupChat: agentHubViewModel.currentMode == .group)
        }
        
        // Process the message
        if let message = streamingService.processStreamResponse(response) {
            if message.type == .assistant && message.role == .assistant {
                // Handle streaming assistant message
                if var assistantMsg = currentAssistantMessage {
                    assistantMsg.content += message.content
                    currentAssistantMessage = assistantMsg
                    agentHubViewModel.updateLastMessage(
                        content: assistantMsg.content,
                        inGroupChat: agentHubViewModel.currentMode == .group
                    )
                } else {
                    currentAssistantMessage = message
                    agentHubViewModel.addMessage(message, toGroupChat: agentHubViewModel.currentMode == .group)
                }
            } else {
                // Handle other message types
                agentHubViewModel.addMessage(message, toGroupChat: agentHubViewModel.currentMode == .group)
                
                if message.type == .system && !hasReceivedInit {
                    hasReceivedInit = true
                }
            }
        }
        
        // Handle special response types
        switch response.type {
        case "done":
            resetRequestState()
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.success.trigger()
            }
        case "aborted":
            resetRequestState()
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.warning.trigger()
            }
        case "error":
            errorMessage = response.error ?? "Unknown error occurred"
            resetRequestState()
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.error.trigger()
            }
        default:
            break
        }
    }
    
    private func handleStreamCompletion(_ result: Result<Void, Error>) {
        switch result {
        case .success:
            resetRequestState()
        case .failure(let error):
            errorMessage = error.localizedDescription
            resetRequestState()
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.error.trigger()
            }
        }
    }
    
    // MARK: - Validation
    private func canSendMessage() -> Bool {
        return !isLoading && 
               agentHubViewModel.canSendMessage(content: inputText) &&
               agentHubViewModel.validateAgentSelection()
    }
    
    // MARK: - Input Management
    func clearInput() {
        inputText = ""
    }
    
    func insertMention(agentId: String) {
        let mention = "@\(agentId) "
        if inputText.isEmpty {
            inputText = mention
        } else {
            inputText += " " + mention
        }
    }
    
    // MARK: - Error Handling
    func clearError() {
        errorMessage = nil
    }
}