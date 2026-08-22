import Foundation

class HistoryService: ObservableObject {
    private let apiService = APIService()
    
    @Published var conversations: [ConversationSummary] = []
    @Published var currentConversation: ConversationHistory?
    @Published var isLoading = false
    @Published var errorMessage: String?
    
    func loadConversationHistories(baseURL: String, projectName: String) {
        isLoading = true
        errorMessage = nil
        
        Task {
            do {
                let histories = try await apiService.fetchConversationHistories(
                    baseURL: baseURL,
                    encodedProjectName: projectName
                )
                
                await MainActor.run {
                    self.conversations = histories.sorted { $0.lastDate ?? Date.distantPast > $1.lastDate ?? Date.distantPast }
                    self.isLoading = false
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = error.localizedDescription
                    self.isLoading = false
                }
            }
        }
    }
    
    func loadConversationDetail(baseURL: String, projectName: String, sessionId: String) {
        isLoading = true
        errorMessage = nil
        
        Task {
            do {
                let conversation = try await apiService.fetchConversationHistory(
                    baseURL: baseURL,
                    encodedProjectName: projectName,
                    sessionId: sessionId
                )
                
                await MainActor.run {
                    self.currentConversation = conversation
                    self.isLoading = false
                }
            } catch {
                await MainActor.run {
                    self.errorMessage = error.localizedDescription
                    self.isLoading = false
                }
            }
        }
    }
    
    func clearCurrentConversation() {
        currentConversation = nil
    }
    
    func formatTimestamp(_ dateString: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: dateString) else {
            return dateString
        }
        
        let displayFormatter = DateFormatter()
        displayFormatter.dateStyle = .medium
        displayFormatter.timeStyle = .short
        
        return displayFormatter.string(from: date)
    }
    
    func timeAgo(from dateString: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: dateString) else {
            return "Unknown"
        }
        
        let now = Date()
        let timeInterval = now.timeIntervalSince(date)
        
        if timeInterval < 60 {
            return "Just now"
        } else if timeInterval < 3600 {
            let minutes = Int(timeInterval / 60)
            return "\(minutes)m ago"
        } else if timeInterval < 86400 {
            let hours = Int(timeInterval / 3600)
            return "\(hours)h ago"
        } else {
            let days = Int(timeInterval / 86400)
            return "\(days)d ago"
        }
    }
}