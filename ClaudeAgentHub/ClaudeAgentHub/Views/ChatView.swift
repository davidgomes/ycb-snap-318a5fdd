import SwiftUI

struct ChatView: View {
    @EnvironmentObject var appSettings: AppSettings
    @StateObject private var agentHubViewModel: AgentHubViewModel
    @StateObject private var chatViewModel: ChatViewModel
    
    @State private var showingHistory = false
    @State private var showingAgentSelector = false
    
    init() {
        // Create temporary instances for initialization - they will be updated in onAppear
        let tempSettings = AppSettings()
        let agentHubVM = AgentHubViewModel(appSettings: tempSettings)
        let chatVM = ChatViewModel(appSettings: tempSettings, agentHubViewModel: agentHubVM)
        
        _agentHubViewModel = StateObject(wrappedValue: agentHubVM)
        _chatViewModel = StateObject(wrappedValue: chatVM)
    }
    
    var body: some View {
        NavigationView {
            VStack(spacing: 0) {
                // Chat Header
                ChatHeaderView()
                
                // Messages Area
                messagesArea
                
                // Chat Input
                ChatInputView()
            }
            .navigationTitle("Chat")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button(action: { showingHistory = true }) {
                        Image(systemName: Constants.Icons.history)
                    }
                }
                
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button(action: { showingAgentSelector = true }) {
                        Image(systemName: Constants.Icons.agents)
                    }
                }
            }
        }
        .onAppear {
            agentHubViewModel.appSettings = appSettings
            chatViewModel.appSettings = appSettings
        }
        .sheet(isPresented: $showingHistory) {
            ConversationHistoryView()
                .environmentObject(appSettings)
        }
        .sheet(isPresented: $showingAgentSelector) {
            AgentSelectorSheet()
        }
        .alert("Error", isPresented: .constant(chatViewModel.errorMessage != nil)) {
            Button("OK") {
                chatViewModel.clearError()
            }
        } message: {
            if let errorMessage = chatViewModel.errorMessage {
                Text(errorMessage)
            }
        }
    }
    
    // MARK: - Chat Header
    @ViewBuilder
    private func ChatHeaderView() -> some View {
        VStack(spacing: 12) {
            // Mode and agent selection
            HStack {
                Picker("Mode", selection: $agentHubViewModel.currentMode) {
                    Text(Constants.Strings.groupMode).tag(ChatMode.group)
                    Text("Individual").tag(ChatMode.individual)
                }
                .pickerStyle(SegmentedPickerStyle())
                
                Spacer()
                
                // Connection status
                HStack(spacing: 4) {
                    Circle()
                        .fill(agentHubViewModel.getCurrentSessionId() != nil ? Constants.Colors.success : Constants.Colors.secondary)
                        .frame(width: 8, height: 8)
                    
                    Text(agentHubViewModel.getCurrentSessionId() != nil ? "Connected" : "Not connected")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }
            
            // Active agent info
            if agentHubViewModel.currentMode == .individual {
                if let activeAgentId = agentHubViewModel.activeAgentId,
                   let agent = agentHubViewModel.getAgent(withId: activeAgentId) {
                    AgentInfoBanner(agent: agent)
                } else {
                    NoAgentSelectedBanner()
                }
            } else {
                if let orchestrator = agentHubViewModel.getOrchestratorAgent() {
                    AgentInfoBanner(agent: orchestrator)
                } else {
                    Text("No orchestrator agent configured")
                        .font(.caption)
                        .foregroundColor(Constants.Colors.warning)
                        .padding(.vertical, 8)
                        .padding(.horizontal, 12)
                        .background(Constants.Colors.warning.opacity(0.1))
                        .cornerRadius(Constants.Dimensions.smallCornerRadius)
                }
            }
        }
        .padding()
        .background(Color.cardBackground)
        .overlay(
            Rectangle()
                .frame(height: 1)
                .foregroundColor(Color.secondary.opacity(0.2)),
            alignment: .bottom
        )
    }
    
    // MARK: - Messages Area
    @ViewBuilder
    private var messagesArea: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 16) {
                    let messages = agentHubViewModel.getMessages()
                    
                    if messages.isEmpty {
                        EmptyStateView()
                    } else {
                        ForEach(messages) { message in
                            MessageBubbleView(
                                message: message,
                                showAgentName: agentHubViewModel.currentMode == .group
                            )
                            .id(message.id)
                        }
                        
                        // Loading indicator
                        if chatViewModel.isLoading {
                            HStack {
                                ProgressView()
                                    .scaleEffect(0.8)
                                Text("Thinking...")
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                                Spacer()
                            }
                            .padding(.horizontal)
                        }
                    }
                }
                .padding(.vertical)
            }
            .background(Color.chatBackground)
            .onChange(of: agentHubViewModel.getMessages().count) { _ in
                if appSettings.autoScrollEnabled, let lastMessage = agentHubViewModel.getMessages().last {
                    withAnimation(.easeOut(duration: 0.3)) {
                        proxy.scrollTo(lastMessage.id, anchor: .bottom)
                    }
                }
            }
        }
    }
    
    // MARK: - Chat Input
    @ViewBuilder
    private func ChatInputView() -> some View {
        VStack(spacing: 0) {
            Divider()
            
            HStack(alignment: .bottom, spacing: 12) {
                // Text input
                VStack(alignment: .leading, spacing: 4) {
                    // Quick agent mentions (in group mode)
                    if agentHubViewModel.currentMode == .group && !appSettings.getWorkerAgents().isEmpty {
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 8) {
                                ForEach(appSettings.getWorkerAgents()) { agent in
                                    Button("@\(agent.id)") {
                                        chatViewModel.insertMention(agentId: agent.id)
                                    }
                                    .font(.caption)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 4)
                                    .background(Constants.Colors.primary.opacity(0.1))
                                    .foregroundColor(Constants.Colors.primary)
                                    .cornerRadius(Constants.Dimensions.smallCornerRadius)
                                }
                            }
                            .padding(.horizontal)
                        }
                    }
                    
                    HStack(alignment: .bottom, spacing: 8) {
                        TextField("Type a message...", text: $chatViewModel.inputText, axis: .vertical)
                            .textFieldStyle(RoundedBorderTextFieldStyle())
                            .lineLimit(1...5)
                            .disabled(chatViewModel.isLoading)
                        
                        // Send/Stop button
                        Button(action: {
                            if chatViewModel.isLoading {
                                chatViewModel.abortCurrentRequest()
                            } else {
                                chatViewModel.sendMessage()
                            }
                        }) {
                            Image(systemName: chatViewModel.isLoading ? Constants.Icons.stop : Constants.Icons.send)
                                .font(.system(size: 20, weight: .medium))
                                .foregroundColor(.white)
                                .frame(width: 36, height: 36)
                                .background(
                                    Circle()
                                        .fill(chatViewModel.isLoading ? Constants.Colors.error : Constants.Colors.primary)
                                )
                        }
                        .disabled(!canSendMessage && !chatViewModel.isLoading)
                    }
                }
            }
            .padding()
            .background(Color.cardBackground)
        }
    }
    
    // MARK: - Supporting Views
    private struct AgentInfoBanner: View {
        let agent: Agent
        
        var body: some View {
            HStack(spacing: 8) {
                Image(systemName: agent.isOrchestrator ? Constants.Icons.orchestrator : Constants.Icons.agent)
                    .foregroundColor(agent.isOrchestrator ? Constants.Colors.accent : Constants.Colors.primary)
                
                VStack(alignment: .leading, spacing: 2) {
                    Text(agent.name)
                        .font(.caption)
                        .fontWeight(.medium)
                    Text(agent.description)
                        .font(.caption2)
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
                
                Spacer()
                
                if agent.isOrchestrator {
                    Text("Orchestrator")
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Constants.Colors.accent.opacity(0.1))
                        .foregroundColor(Constants.Colors.accent)
                        .cornerRadius(4)
                }
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 12)
            .background(Constants.Colors.primary.opacity(0.05))
            .cornerRadius(Constants.Dimensions.smallCornerRadius)
        }
    }
    
    private struct NoAgentSelectedBanner: View {
        var body: some View {
            HStack {
                Image(systemName: "exclamationmark.triangle")
                    .foregroundColor(Constants.Colors.warning)
                Text("No agent selected")
                    .font(.caption)
                    .fontWeight(.medium)
                Spacer()
                Text("Tap agents icon to select")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 12)
            .background(Constants.Colors.warning.opacity(0.1))
            .cornerRadius(Constants.Dimensions.smallCornerRadius)
        }
    }
    
    private struct EmptyStateView: View {
        var body: some View {
            VStack(spacing: 16) {
                Image(systemName: "message")
                    .font(.system(size: 50))
                    .foregroundColor(.secondary)
                
                Text("Start a conversation")
                    .font(.headline)
                    .fontWeight(.medium)
                
                Text("Send a message to begin chatting with your agents")
                    .font(.body)
                    .foregroundColor(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 32)
            }
            .padding(.top, 50)
        }
    }
    
    private struct AgentSelectorSheet: View {
        @Environment(\.dismiss) private var dismiss
        
        var body: some View {
            NavigationView {
                AgentHubView()
                    .navigationTitle("Select Agent")
                    .navigationBarTitleDisplayMode(.inline)
                    .toolbar {
                        ToolbarItem(placement: .navigationBarTrailing) {
                            Button("Done") {
                                dismiss()
                            }
                        }
                    }
            }
        }
    }
    
    private struct ConversationHistoryView: View {
        @EnvironmentObject var appSettings: AppSettings
        @Environment(\.dismiss) private var dismiss
        @StateObject private var historyService = HistoryService()
        
        var body: some View {
            NavigationView {
                VStack {
                    if historyService.isLoading {
                        ProgressView("Loading history...")
                    } else if historyService.conversations.isEmpty {
                        Text("No conversation history")
                            .foregroundColor(.secondary)
                    } else {
                        List(historyService.conversations) { conversation in
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Session: \(conversation.sessionId)")
                                    .font(.headline)
                                Text("\(conversation.messageCount) messages")
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                                Text(conversation.lastMessagePreview)
                                    .font(.body)
                                    .lineLimit(2)
                            }
                            .padding(.vertical, 4)
                        }
                    }
                }
                .navigationTitle("History")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .navigationBarTrailing) {
                        Button("Done") {
                            dismiss()
                        }
                    }
                }
                .onAppear {
                    if let projectPath = appSettings.selectedProjectPath {
                        let url = URL(fileURLWithPath: projectPath)
                        historyService.loadConversationHistories(
                            baseURL: appSettings.apiBaseURL,
                            projectName: url.lastPathComponent
                        )
                    }
                }
            }
        }
    }
    
    // MARK: - Computed Properties
    private var canSendMessage: Bool {
        return !chatViewModel.inputText.trimmed().isEmpty && 
               agentHubViewModel.validateAgentSelection()
    }
}

#Preview {
    ChatView()
        .environmentObject(AppSettings())
}