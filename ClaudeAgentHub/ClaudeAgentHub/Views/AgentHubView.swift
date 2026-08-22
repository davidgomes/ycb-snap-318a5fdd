import SwiftUI

struct AgentHubView: View {
    @EnvironmentObject var appSettings: AppSettings
    @StateObject private var agentHubViewModel: AgentHubViewModel
    @StateObject private var chatViewModel: ChatViewModel
    
    @State private var showingNewChatAlert = false
    
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
                // Header with mode toggle
                HeaderView()
                
                // Main content based on mode
                if agentHubViewModel.currentMode == .group {
                    GroupChatView()
                } else {
                    IndividualAgentView()
                }
            }
            .navigationTitle(Constants.Strings.agentsTab)
            .navigationBarTitleDisplayMode(.large)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button(action: { showingNewChatAlert = true }) {
                        Image(systemName: Constants.Icons.add)
                    }
                }
            }
        }
        .onAppear {
            // Update view models with current settings
            agentHubViewModel.appSettings = appSettings
            chatViewModel.appSettings = appSettings
        }
        .alert("New Chat", isPresented: $showingNewChatAlert) {
            Button("Orchestrator") {
                startNewGroupChat()
            }
            if !appSettings.getWorkerAgents().isEmpty {
                Button("Individual Chat") {
                    // Show agent selection
                }
            }
            Button("Cancel", role: .cancel) { }
        } message: {
            Text("Choose the type of conversation to start")
        }
    }
    
    // MARK: - Header View
    @ViewBuilder
    private func HeaderView() -> some View {
        HStack {
            Picker("Mode", selection: $agentHubViewModel.currentMode) {
                Text(Constants.Strings.groupMode).tag(ChatMode.group)
                Text("Individual").tag(ChatMode.individual)
            }
            .pickerStyle(SegmentedPickerStyle())
            
            Spacer()
            
            // Active session indicator
            if agentHubViewModel.getCurrentSessionId() != nil {
                Circle()
                    .fill(Constants.Colors.success)
                    .frame(width: 8, height: 8)
            }
        }
        .padding()
        .background(Color.cardBackground)
    }
    
    // MARK: - Orchestrator View
    @ViewBuilder
    private func GroupChatView() -> some View {
        VStack(spacing: 0) {
            // Orchestrator info
            if let orchestrator = agentHubViewModel.getOrchestratorAgent() {
                AgentCardView(agent: orchestrator, isSelected: true)
                    .padding(.horizontal)
            } else {
                VStack {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 50))
                        .foregroundColor(Constants.Colors.warning)
                    Text("No Orchestrator Agent")
                        .font(.headline)
                    Text("Configure an orchestrator agent in Settings to use orchestration")
                        .font(.caption)
                        .multilineTextAlignment(.center)
                        .foregroundColor(.secondary)
                }
                .padding()
            }
            
            Divider()
            
            // Available worker agents
            VStack(alignment: .leading) {
                Text("Available Agents")
                    .font(.headline)
                    .padding(.horizontal)
                
                if appSettings.getWorkerAgents().isEmpty {
                    Text("No worker agents configured")
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .padding(.horizontal)
                } else {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 12) {
                            ForEach(appSettings.getWorkerAgents()) { agent in
                                AgentCardView(agent: agent, isSelected: false)
                                    .frame(width: 200)
                            }
                        }
                        .padding(.horizontal)
                    }
                }
            }
            
            Spacer()
            
            // Recent group messages preview
            if !agentHubViewModel.groupChatMessages.isEmpty {
                VStack(alignment: .leading) {
                    Text("Recent Activity")
                        .font(.headline)
                        .padding(.horizontal)
                    
                    LazyVStack {
                        ForEach(agentHubViewModel.groupChatMessages.suffix(3)) { message in
                            MessagePreviewView(message: message)
                        }
                    }
                    .padding(.horizontal)
                }
            }
        }
    }
    
    // MARK: - Individual Agent View
    @ViewBuilder
    private func IndividualAgentView() -> some View {
        VStack {
            // Agent selection
            if appSettings.agents.isEmpty {
                VStack {
                    Image(systemName: "person.3")
                        .font(.system(size: 50))
                        .foregroundColor(.secondary)
                    Text("No Agents Available")
                        .font(.headline)
                    Text("Add agents in Settings to get started")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                .padding()
            } else {
                ScrollView {
                    LazyVStack(spacing: 12) {
                        ForEach(appSettings.agents) { agent in
                            AgentCardView(
                                agent: agent,
                                isSelected: agentHubViewModel.activeAgentId == agent.id
                            )
                            .onTapGesture {
                                agentHubViewModel.switchToIndividualMode(agentId: agent.id)
                                if appSettings.enableHapticFeedback {
                                    Constants.HapticFeedback.light.trigger()
                                }
                            }
                        }
                    }
                    .padding()
                }
            }
            
            Spacer()
        }
    }
    
    // MARK: - Message Preview
    private struct MessagePreviewView: View {
        let message: ChatMessage
        
        var body: some View {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(message.agentId ?? "Unknown")
                            .font(.caption)
                            .fontWeight(.medium)
                        Spacer()
                        Text(message.timestamp.timeAgoDisplay())
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                    
                    Text(message.displayContent)
                        .font(.caption)
                        .lineLimit(2)
                        .foregroundColor(.primary)
                }
                Spacer()
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 12)
            .background(Color.messageBackground)
            .cornerRadius(Constants.Dimensions.smallCornerRadius)
        }
    }
    
    // MARK: - Actions
    private func startNewGroupChat() {
        agentHubViewModel.switchToGroupMode()
        agentHubViewModel.clearSession()
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.success.trigger()
        }
    }
}

#Preview {
    AgentHubView()
        .environmentObject(AppSettings())
}