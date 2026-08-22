import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var appSettings: AppSettings
    @StateObject private var settingsViewModel: SettingsViewModel
    
    @State private var showingAgentForm = false
    @State private var showingDeleteConfirmation = false
    @State private var agentToDelete: Agent?
    
    init() {
        // Create temporary instance for initialization - it will be updated in onAppear
        let tempSettings = AppSettings()
        _settingsViewModel = StateObject(wrappedValue: SettingsViewModel(appSettings: tempSettings))
    }
    
    var body: some View {
        NavigationView {
            List {
                // API Configuration Section
                Section("API Configuration") {
                    HStack {
                        Text("Base URL")
                        Spacer()
                        TextField("http://localhost:8080", text: $appSettings.apiBaseURL)
                            .textFieldStyle(RoundedBorderTextFieldStyle())
                            .frame(width: 200)
                            .onSubmit {
                                settingsViewModel.updateAPIBaseURL(appSettings.apiBaseURL)
                            }
                    }
                    
                    Button("Test Connection") {
                        settingsViewModel.testAPIConnection()
                    }
                    .foregroundColor(Constants.Colors.primary)
                }
                
                // Project Selection Section
                Section("Project Directory") {
                    if let selectedPath = appSettings.selectedProjectPath {
                        HStack {
                            VStack(alignment: .leading) {
                                Text("Current Project")
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                                Text(URL(fileURLWithPath: selectedPath).lastPathComponent)
                                    .font(.body)
                                    .fontWeight(.medium)
                            }
                            Spacer()
                            Button("Change") {
                                settingsViewModel.loadAvailableProjects()
                            }
                            .foregroundColor(Constants.Colors.primary)
                        }
                    } else {
                        Button("Select Project Directory") {
                            settingsViewModel.loadAvailableProjects()
                        }
                        .foregroundColor(Constants.Colors.primary)
                    }
                    
                    if settingsViewModel.isLoadingProjects {
                        HStack {
                            ProgressView()
                                .scaleEffect(0.8)
                            Text("Loading projects...")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                    }
                }
                
                // Agents Section
                Section("Agents") {
                    ForEach(appSettings.agents) { agent in
                        AgentSettingsRow(
                            agent: agent,
                            onEdit: {
                                settingsViewModel.startEditingAgent(agent)
                                showingAgentForm = true
                            },
                            onDelete: {
                                agentToDelete = agent
                                showingDeleteConfirmation = true
                            }
                        )
                    }
                    
                    Button(action: {
                        settingsViewModel.startAddingAgent()
                        showingAgentForm = true
                    }) {
                        HStack {
                            Image(systemName: Constants.Icons.add)
                            Text("Add Agent")
                        }
                        .foregroundColor(Constants.Colors.primary)
                    }
                }
                
                // App Preferences Section
                Section("Preferences") {
                    Toggle("Dark Mode", isOn: $appSettings.isDarkMode)
                        .onChange(of: appSettings.isDarkMode) { _ in
                            settingsViewModel.toggleDarkMode()
                        }
                    
                    Toggle("Haptic Feedback", isOn: $appSettings.enableHapticFeedback)
                        .onChange(of: appSettings.enableHapticFeedback) { _ in
                            settingsViewModel.toggleHapticFeedback()
                        }
                    
                    Toggle("Auto Scroll", isOn: $appSettings.autoScrollEnabled)
                        .onChange(of: appSettings.autoScrollEnabled) { _ in
                            settingsViewModel.toggleAutoScroll()
                        }
                }
                
                // Data Management Section
                Section("Data Management") {
                    Button("Export Agent Configuration") {
                        if let data = settingsViewModel.exportAgentConfiguration() {
                            // Handle export (could save to Files app or share)
                        }
                    }
                    .foregroundColor(Constants.Colors.primary)
                    
                    Button("Import Agent Configuration") {
                        // Handle import (could use document picker)
                    }
                    .foregroundColor(Constants.Colors.primary)
                }
                
                // About Section
                Section("About") {
                    HStack {
                        Text("Version")
                        Spacer()
                        Text("1.0.0")
                            .foregroundColor(.secondary)
                    }
                    
                    Link("GitHub Repository", destination: URL(string: "https://github.com/your-repo")!)
                        .foregroundColor(Constants.Colors.primary)
                }
            }
            .navigationTitle(Constants.Strings.settingsTab)
            .navigationBarTitleDisplayMode(.large)
        }
        .onAppear {
            settingsViewModel.appSettings = appSettings
        }
        .sheet(isPresented: $showingAgentForm) {
            AgentFormView(viewModel: settingsViewModel)
        }
        .sheet(isPresented: .constant(!settingsViewModel.availableProjects.isEmpty)) {
            ProjectSelectionSheet(viewModel: settingsViewModel)
        }
        .alert("Delete Agent", isPresented: $showingDeleteConfirmation) {
            Button("Delete", role: .destructive) {
                if let agent = agentToDelete {
                    settingsViewModel.deleteAgent(agent)
                    agentToDelete = nil
                }
            }
            Button("Cancel", role: .cancel) {
                agentToDelete = nil
            }
        } message: {
            if let agent = agentToDelete {
                Text("Are you sure you want to delete '\(agent.name)'? This action cannot be undone.")
            }
        }
        .alert("Error", isPresented: .constant(settingsViewModel.errorMessage != nil)) {
            Button("OK") {
                settingsViewModel.clearError()
            }
        } message: {
            if let errorMessage = settingsViewModel.errorMessage {
                Text(errorMessage)
            }
        }
    }
}

// MARK: - Agent Settings Row
private struct AgentSettingsRow: View {
    let agent: Agent
    let onEdit: () -> Void
    let onDelete: () -> Void
    
    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(agent.name)
                        .font(.headline)
                        .fontWeight(.medium)
                    
                    if agent.isOrchestrator {
                        Text("Orchestrator")
                            .font(.caption2)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Constants.Colors.accent.opacity(0.1))
                            .foregroundColor(Constants.Colors.accent)
                            .cornerRadius(4)
                    }
                    
                    Spacer()
                }
                
                Text(agent.description)
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                
                HStack {
                    Circle()
                        .fill(agent.isEnabled ? Constants.Colors.success : Constants.Colors.secondary)
                        .frame(width: 6, height: 6)
                    
                    Text(agent.isEnabled ? "Enabled" : "Disabled")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }
            
            Spacer()
            
            // Action buttons
            HStack(spacing: 8) {
                Button(action: onEdit) {
                    Image(systemName: Constants.Icons.edit)
                        .foregroundColor(Constants.Colors.primary)
                }
                
                Button(action: onDelete) {
                    Image(systemName: Constants.Icons.delete)
                        .foregroundColor(Constants.Colors.error)
                }
            }
        }
        .padding(.vertical, 4)
    }
}

// MARK: - Agent Form View
private struct AgentFormView: View {
    @ObservedObject var viewModel: SettingsViewModel
    @Environment(\.dismiss) private var dismiss
    
    var body: some View {
        NavigationView {
            Form {
                Section("Basic Information") {
                    TextField("Agent Name", text: $viewModel.agentName)
                    TextField("Description", text: $viewModel.agentDescription, axis: .vertical)
                        .lineLimit(2...4)
                }
                
                Section("Configuration") {
                    TextField("Working Directory", text: $viewModel.agentWorkingDirectory)
                    TextField("API Endpoint", text: $viewModel.agentAPIEndpoint)
                        .keyboardType(.URL)
                        .autocapitalization(.none)
                }
                
                Section("Settings") {
                    Toggle("Is Orchestrator", isOn: $viewModel.agentIsOrchestrator)
                    Toggle("Enabled", isOn: $viewModel.agentIsEnabled)
                }
                
                if viewModel.agentIsOrchestrator {
                    Section {
                        Text("Orchestrator agents coordinate multi-agent workflows and handle task decomposition.")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                }
            }
            .navigationTitle(viewModel.isAddingNewAgent ? "Add Agent" : "Edit Agent")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("Cancel") {
                        viewModel.cancelAgentEditing()
                        dismiss()
                    }
                }
                
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Save") {
                        viewModel.saveAgent()
                        dismiss()
                    }
                    .disabled(!canSave)
                }
            }
        }
    }
    
    private var canSave: Bool {
        return !viewModel.agentName.trimmed().isEmpty &&
               !viewModel.agentDescription.trimmed().isEmpty &&
               !viewModel.agentWorkingDirectory.trimmed().isEmpty &&
               !viewModel.agentAPIEndpoint.trimmed().isEmpty
    }
}

// MARK: - Project Selection Sheet
private struct ProjectSelectionSheet: View {
    @ObservedObject var viewModel: SettingsViewModel
    @Environment(\.dismiss) private var dismiss
    
    var body: some View {
        NavigationView {
            List(viewModel.availableProjects) { project in
                VStack(alignment: .leading, spacing: 4) {
                    Text(project.name)
                        .font(.headline)
                    Text(project.path)
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                .padding(.vertical, 4)
                .onTapGesture {
                    viewModel.selectProject(project)
                    dismiss()
                }
            }
            .navigationTitle("Select Project")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
        }
    }
}

#Preview {
    SettingsView()
        .environmentObject(AppSettings())
}