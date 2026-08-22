import Foundation

struct Project: Codable, Identifiable, Hashable {
    let id: String
    let path: String
    let encodedName: String
    let name: String
    
    init(path: String, encodedName: String) {
        self.id = encodedName
        self.path = path
        self.encodedName = encodedName
        
        // Extract directory name from path for display
        let url = URL(fileURLWithPath: path)
        self.name = url.lastPathComponent
    }
    
    static let sampleProjects: [Project] = [
        Project(path: "/Users/user/projects/my-app", encodedName: "my-app"),
        Project(path: "/Users/user/projects/backend-api", encodedName: "backend-api"),
        Project(path: "/Users/user/projects/mobile-app", encodedName: "mobile-app")
    ]
}

struct ProjectsResponse: Codable {
    let projects: [Project]
}

struct ConversationSummary: Codable, Identifiable {
    let id: String
    let sessionId: String
    let startTime: String
    let lastTime: String
    let messageCount: Int
    let lastMessagePreview: String
    
    init(sessionId: String, startTime: String, lastTime: String, messageCount: Int, lastMessagePreview: String) {
        self.id = sessionId
        self.sessionId = sessionId
        self.startTime = startTime
        self.lastTime = lastTime
        self.messageCount = messageCount
        self.lastMessagePreview = lastMessagePreview
    }
    
    var startDate: Date? {
        return ISO8601DateFormatter().date(from: startTime)
    }
    
    var lastDate: Date? {
        return ISO8601DateFormatter().date(from: lastTime)
    }
}

struct HistoryListResponse: Codable {
    let conversations: [ConversationSummary]
}

struct ConversationHistory: Codable {
    let sessionId: String
    let messages: [ChatMessage]
    let metadata: ConversationMetadata
}

struct ConversationMetadata: Codable {
    let startTime: String
    let endTime: String?
    let messageCount: Int
}