import Foundation
import Combine

class SettingsViewModel: ObservableObject {
    @Published var isEditingAgent = false
    @Published var editingAgent: Agent?
    @Published var isAddingNewAgent = false
    @Published var isLoadingProjects = false
    @Published var availableProjects: [Project] = []
    @Published var errorMessage: String?
    
    // Form fields for agent editing
    @Published var agentName = ""
    @Published var agentDescription = ""
    @Published var agentWorkingDirectory = ""
    @Published var agentAPIEndpoint = ""
    @Published var agentIsOrchestrator = false
    @Published var agentIsEnabled = true
    
    private let apiService = APIService()
    var appSettings: AppSettings
    private var cancellables = Set<AnyCancellable>()
    
    init(appSettings: AppSettings) {
        self.appSettings = appSettings
    }
    
    // MARK: - Project Management
    func loadAvailableProjects() {
        isLoadingProjects = true
        errorMessage = nil
        
        Task {
            do {
                let projects = try await apiService.fetchProjects(baseURL: appSettings.apiBaseURL)
                
                await MainActor.run {
                    self.availableProjects = projects
                    self.isLoadingProjects = false
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = "Failed to load projects: \(error.localizedDescription)"
                    self.isLoadingProjects = false
                }
            }
        }
    }
    
    func selectProject(_ project: Project) {
        appSettings.selectedProjectPath = project.path
        appSettings.saveSelectedProjectPath()
    }
    
    // MARK: - Agent Management
    func startAddingAgent() {
        clearAgentForm()
        isAddingNewAgent = true
        isEditingAgent = true
    }
    
    func startEditingAgent(_ agent: Agent) {
        editingAgent = agent
        loadAgentIntoForm(agent)
        isAddingNewAgent = false
        isEditingAgent = true
    }
    
    func saveAgent() {
        guard validateAgentForm() else { return }
        
        let agentId = editingAgent?.id ?? UUID().uuidString
        
        let agent = Agent(
            id: agentId,
            name: agentName.trimmed(),
            description: agentDescription.trimmed(),
            workingDirectory: agentWorkingDirectory.trimmed(),
            apiEndpoint: agentAPIEndpoint.trimmed(),
            isOrchestrator: agentIsOrchestrator,
            isEnabled: agentIsEnabled
        )
        
        if isAddingNewAgent {
            appSettings.addAgent(agent)
        } else {
            appSettings.updateAgent(agent)
        }
        
        cancelAgentEditing()
        
        // Trigger haptic feedback
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.success.trigger()
        }
    }
    
    func deleteAgent(_ agent: Agent) {
        appSettings.deleteAgent(withId: agent.id)
        
        // Trigger haptic feedback
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.warning.trigger()
        }
    }
    
    func cancelAgentEditing() {
        isEditingAgent = false
        editingAgent = nil
        isAddingNewAgent = false
        clearAgentForm()
    }
    
    private func loadAgentIntoForm(_ agent: Agent) {
        agentName = agent.name
        agentDescription = agent.description
        agentWorkingDirectory = agent.workingDirectory
        agentAPIEndpoint = agent.apiEndpoint
        agentIsOrchestrator = agent.isOrchestrator
        agentIsEnabled = agent.isEnabled
    }
    
    private func clearAgentForm() {
        agentName = ""
        agentDescription = ""
        agentWorkingDirectory = ""
        agentAPIEndpoint = ""
        agentIsOrchestrator = false
        agentIsEnabled = true
    }
    
    private func validateAgentForm() -> Bool {
        let trimmedName = agentName.trimmed()
        let trimmedDescription = agentDescription.trimmed()
        let trimmedWorkingDirectory = agentWorkingDirectory.trimmed()
        let trimmedAPIEndpoint = agentAPIEndpoint.trimmed()
        
        guard !trimmedName.isEmpty else {
            errorMessage = "Agent name cannot be empty"
            return false
        }
        
        guard !trimmedDescription.isEmpty else {
            errorMessage = "Agent description cannot be empty"
            return false
        }
        
        guard !trimmedWorkingDirectory.isEmpty else {
            errorMessage = "Working directory cannot be empty"
            return false
        }
        
        guard !trimmedAPIEndpoint.isEmpty else {
            errorMessage = "API endpoint cannot be empty"
            return false
        }
        
        guard URL(string: trimmedAPIEndpoint) != nil else {
            errorMessage = "Invalid API endpoint URL"
            return false
        }
        
        // Check for duplicate names (excluding current agent if editing)
        let existingAgent = appSettings.agents.first { $0.name == trimmedName }
        if let existing = existingAgent, existing.id != editingAgent?.id {
            errorMessage = "An agent with this name already exists"
            return false
        }
        
        // Validate orchestrator constraints
        if agentIsOrchestrator {
            let existingOrchestrator = appSettings.agents.first { $0.isOrchestrator && $0.id != editingAgent?.id }
            if existingOrchestrator != nil {
                errorMessage = "Only one orchestrator agent is allowed"
                return false
            }
        }
        
        return true
    }
    
    // MARK: - API Settings
    func updateAPIBaseURL(_ newURL: String) {
        guard let url = URL(string: newURL.trimmed()), !newURL.trimmed().isEmpty else {
            errorMessage = "Invalid API base URL"
            return
        }
        
        appSettings.apiBaseURL = newURL.trimmed()
        appSettings.saveAPIBaseURL()
        
        // Reload projects with new URL
        loadAvailableProjects()
    }
    
    func testAPIConnection() {
        Task {
            do {
                _ = try await apiService.fetchProjects(baseURL: appSettings.apiBaseURL)
                
                await MainActor.run {
                    // Show success feedback
                    if self.appSettings.enableHapticFeedback {
                        Constants.HapticFeedback.success.trigger()
                    }
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = "API connection failed: \(error.localizedDescription)"
                    if self.appSettings.enableHapticFeedback {
                        Constants.HapticFeedback.error.trigger()
                    }
                }
            }
        }
    }
    
    // MARK: - App Settings
    func toggleDarkMode() {
        appSettings.isDarkMode.toggle()
        appSettings.saveAppearanceSettings()
    }
    
    func toggleHapticFeedback() {
        appSettings.enableHapticFeedback.toggle()
        appSettings.saveUserPreferences()
        
        // Provide immediate feedback
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.light.trigger()
        }
    }
    
    func toggleAutoScroll() {
        appSettings.autoScrollEnabled.toggle()
        appSettings.saveUserPreferences()
    }
    
    // MARK: - Error Handling
    func clearError() {
        errorMessage = nil
    }
    
    // MARK: - Data Export/Import
    func exportAgentConfiguration() -> Data? {
        do {
            let configData = try JSONEncoder().encode(appSettings.agents)
            return configData
        } catch {
            errorMessage = "Failed to export configuration: \(error.localizedDescription)"
            return nil
        }
    }
    
    func importAgentConfiguration(from data: Data) {
        do {
            let importedAgents = try JSONDecoder().decode([Agent].self, from: data)
            
            // Validate imported agents
            var validAgents: [Agent] = []
            var orchestratorCount = 0
            
            for agent in importedAgents {
                if agent.isOrchestrator {
                    orchestratorCount += 1
                    if orchestratorCount > 1 {
                        continue // Skip extra orchestrators
                    }
                }
                validAgents.append(agent)
            }
            
            appSettings.agents = validAgents
            appSettings.saveAgents()
            
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.success.trigger()
            }
        } catch {
            errorMessage = "Failed to import configuration: \(error.localizedDescription)"
            if appSettings.enableHapticFeedback {
                Constants.HapticFeedback.error.trigger()
            }
        }
    }
}