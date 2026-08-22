import SwiftUI

struct ContentView: View {
    @StateObject private var appSettings = AppSettings()
    @State private var selectedTab = 0
    
    var body: some View {
        TabView(selection: $selectedTab) {
            // Agent Hub Tab
            AgentHubView()
                .environmentObject(appSettings)
                .tabItem {
                    Image(systemName: Constants.Icons.agents)
                    Text(Constants.Strings.agentsTab)
                }
                .tag(0)
            
            // Chat Tab
            ChatView()
                .environmentObject(appSettings)
                .tabItem {
                    Image(systemName: Constants.Icons.chat)
                    Text(Constants.Strings.chatTab)
                }
                .tag(1)
            
            // Projects Tab
            ProjectSelectorView()
                .environmentObject(appSettings)
                .tabItem {
                    Image(systemName: Constants.Icons.projects)
                    Text(Constants.Strings.projectsTab)
                }
                .tag(2)
            
            // Settings Tab
            SettingsView()
                .environmentObject(appSettings)
                .tabItem {
                    Image(systemName: Constants.Icons.settings)
                    Text(Constants.Strings.settingsTab)
                }
                .tag(3)
        }
        .accentColor(Constants.Colors.primary)
        .preferredColorScheme(appSettings.isDarkMode ? .dark : .light)
    }
}

#Preview {
    ContentView()
}