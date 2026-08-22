import SwiftUI

struct ProjectSelectorView: View {
    @EnvironmentObject var appSettings: AppSettings
    @StateObject private var apiService = APIService()
    
    @State private var availableProjects: [Project] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var showingCustomPathAlert = false
    @State private var customPath = ""
    
    var body: some View {
        NavigationView {
            VStack(spacing: 0) {
                // Current project header
                if let selectedPath = appSettings.selectedProjectPath {
                    CurrentProjectHeader(projectPath: selectedPath)
                } else {
                    NoProjectSelectedHeader()
                }
                
                Divider()
                
                // Available projects list
                if isLoading {
                    LoadingView()
                } else if availableProjects.isEmpty {
                    EmptyStateView(onRetry: loadProjects)
                } else {
                    ProjectListView(
                        projects: availableProjects,
                        selectedPath: appSettings.selectedProjectPath,
                        onProjectSelect: selectProject
                    )
                }
                
                Spacer()
            }
            .navigationTitle(Constants.Strings.projectsTab)
            .navigationBarTitleDisplayMode(.large)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Menu {
                        Button("Refresh Projects") {
                            loadProjects()
                        }
                        
                        Button("Add Custom Path") {
                            showingCustomPathAlert = true
                        }
                        
                        if appSettings.selectedProjectPath != nil {
                            Button("Clear Selection") {
                                clearProjectSelection()
                            }
                        }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                }
            }
        }
        .onAppear {
            loadProjects()
        }
        .alert("Custom Project Path", isPresented: $showingCustomPathAlert) {
            TextField("Project path", text: $customPath)
            Button("Add") {
                addCustomProject()
            }
            Button("Cancel", role: .cancel) { }
        } message: {
            Text("Enter the full path to your project directory")
        }
        .alert("Error", isPresented: .constant(errorMessage != nil)) {
            Button("OK") {
                errorMessage = nil
            }
        } message: {
            if let errorMessage = errorMessage {
                Text(errorMessage)
            }
        }
    }
    
    // MARK: - Actions
    private func loadProjects() {
        isLoading = true
        errorMessage = nil
        
        Task {
            do {
                let projects = try await apiService.fetchProjects(baseURL: appSettings.apiBaseURL)
                
                await MainActor.run {
                    self.availableProjects = projects.sorted { $0.name < $1.name }
                    self.isLoading = false
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = "Failed to load projects: \(error.localizedDescription)"
                    self.isLoading = false
                }
            }
        }
    }
    
    private func selectProject(_ project: Project) {
        appSettings.selectedProjectPath = project.path
        appSettings.saveSelectedProjectPath()
        
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.success.trigger()
        }
    }
    
    private func clearProjectSelection() {
        appSettings.selectedProjectPath = nil
        appSettings.saveSelectedProjectPath()
        
        if appSettings.enableHapticFeedback {
            Constants.HapticFeedback.light.trigger()
        }
    }
    
    private func addCustomProject() {
        let trimmedPath = customPath.trimmed()
        guard !trimmedPath.isEmpty else { return }
        
        let url = URL(fileURLWithPath: trimmedPath)
        let project = Project(path: trimmedPath, encodedName: url.lastPathComponent)
        
        // Add to available projects if not already present
        if !availableProjects.contains(where: { $0.path == trimmedPath }) {
            availableProjects.append(project)
            availableProjects.sort { $0.name < $1.name }
        }
        
        selectProject(project)
        customPath = ""
    }
}

// MARK: - Header Views
private struct CurrentProjectHeader: View {
    let projectPath: String
    
    private var projectName: String {
        URL(fileURLWithPath: projectPath).lastPathComponent
    }
    
    var body: some View {
        VStack(spacing: 8) {
            HStack {
                Image(systemName: "folder.fill")
                    .font(.title2)
                    .foregroundColor(Constants.Colors.primary)
                
                VStack(alignment: .leading, spacing: 2) {
                    Text("Current Project")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Text(projectName)
                        .font(.headline)
                        .fontWeight(.semibold)
                }
                
                Spacer()
                
                Image(systemName: "checkmark.circle.fill")
                    .foregroundColor(Constants.Colors.success)
            }
            
            Text(projectPath)
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
        }
        .padding()
        .background(Constants.Colors.success.opacity(0.1))
        .cornerRadius(Constants.Dimensions.cornerRadius)
        .padding(.horizontal)
        .padding(.top)
    }
}

private struct NoProjectSelectedHeader: View {
    var body: some View {
        VStack(spacing: 8) {
            HStack {
                Image(systemName: "folder")
                    .font(.title2)
                    .foregroundColor(.secondary)
                
                VStack(alignment: .leading, spacing: 2) {
                    Text("No Project Selected")
                        .font(.headline)
                        .fontWeight(.semibold)
                    Text("Choose a project directory to work with")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                
                Spacer()
            }
        }
        .padding()
        .background(Constants.Colors.warning.opacity(0.1))
        .cornerRadius(Constants.Dimensions.cornerRadius)
        .padding(.horizontal)
        .padding(.top)
    }
}

// MARK: - Content Views
private struct LoadingView: View {
    var body: some View {
        VStack(spacing: 16) {
            ProgressView()
                .scaleEffect(1.2)
            Text(Constants.Strings.loadingProjects)
                .font(.body)
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct EmptyStateView: View {
    let onRetry: () -> Void
    
    var body: some View {
        VStack(spacing: 20) {
            Image(systemName: "folder.badge.questionmark")
                .font(.system(size: 60))
                .foregroundColor(.secondary)
            
            Text("No Projects Found")
                .font(.headline)
                .fontWeight(.medium)
            
            Text("Make sure your backend server is running and accessible")
                .font(.body)
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
            
            Button(Constants.Strings.retryButton) {
                onRetry()
            }
            .buttonStyle(.borderedProminent)
            .tint(Constants.Colors.primary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct ProjectListView: View {
    let projects: [Project]
    let selectedPath: String?
    let onProjectSelect: (Project) -> Void
    
    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                ForEach(projects) { project in
                    ProjectCard(
                        project: project,
                        isSelected: project.path == selectedPath,
                        onSelect: {
                            onProjectSelect(project)
                        }
                    )
                }
            }
            .padding()
        }
    }
}

private struct ProjectCard: View {
    let project: Project
    let isSelected: Bool
    let onSelect: () -> Void
    
    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: 12) {
                // Project icon
                Image(systemName: "folder.fill")
                    .font(.title2)
                    .foregroundColor(isSelected ? Constants.Colors.primary : .secondary)
                
                // Project info
                VStack(alignment: .leading, spacing: 4) {
                    Text(project.name)
                        .font(.headline)
                        .fontWeight(.medium)
                        .foregroundColor(.primary)
                        .lineLimit(1)
                    
                    Text(project.path)
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                }
                
                Spacer()
                
                // Selection indicator
                if isSelected {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.title3)
                        .foregroundColor(Constants.Colors.success)
                } else {
                    Image(systemName: "circle")
                        .font(.title3)
                        .foregroundColor(.secondary)
                }
            }
            .padding()
            .background(cardBackground)
            .overlay(
                RoundedRectangle(cornerRadius: Constants.Dimensions.cornerRadius)
                    .stroke(borderColor, lineWidth: isSelected ? 2 : 1)
            )
            .cornerRadius(Constants.Dimensions.cornerRadius)
            .scaleEffect(isSelected ? 1.02 : 1.0)
            .animation(Constants.Animation.spring, value: isSelected)
        }
        .buttonStyle(PlainButtonStyle())
    }
    
    private var cardBackground: Color {
        if isSelected {
            return Constants.Colors.primary.opacity(0.05)
        } else {
            return Color.cardBackground
        }
    }
    
    private var borderColor: Color {
        if isSelected {
            return Constants.Colors.primary
        } else {
            return Color.clear
        }
    }
}

#Preview {
    ProjectSelectorView()
        .environmentObject(AppSettings())
}