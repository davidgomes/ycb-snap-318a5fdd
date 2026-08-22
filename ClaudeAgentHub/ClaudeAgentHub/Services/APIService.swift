import Foundation
import Combine

struct ChatRequest: Codable {
    let message: String
    let sessionId: String?
    let requestId: String
    let workingDirectory: String?
    let availableAgents: [AgentInfo]?
    
    struct AgentInfo: Codable {
        let id: String
        let name: String
        let description: String
        let workingDirectory: String
        let apiEndpoint: String
        let isOrchestrator: Bool?
    }
}

struct StreamResponse: Codable {
    let type: String
    let data: StreamData?
    let error: String?
    
    struct StreamData: Codable {
        let type: String?
        let message: MessageContent?
        let cwd: String?
        let tools: [String]?
        let sessionId: String?
        
        struct MessageContent: Codable {
            let content: [ContentItem]?
            
            struct ContentItem: Codable {
                let type: String
                let text: String?
                let name: String?
                let input: [String: AnyCodable]?
            }
        }
    }
}

struct AnyCodable: Codable {
    let value: Any
    
    init(_ value: Any) {
        self.value = value
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let string = try? container.decode(String.self) {
            value = string
        } else if let int = try? container.decode(Int.self) {
            value = int
        } else if let double = try? container.decode(Double.self) {
            value = double
        } else if let bool = try? container.decode(Bool.self) {
            value = bool
        } else {
            value = ""
        }
    }
    
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        if let string = value as? String {
            try container.encode(string)
        } else if let int = value as? Int {
            try container.encode(int)
        } else if let double = value as? Double {
            try container.encode(double)
        } else if let bool = value as? Bool {
            try container.encode(bool)
        }
    }
}

class APIService: ObservableObject {
    private let session = URLSession.shared
    private var cancellables = Set<AnyCancellable>()
    
    func fetchProjects(baseURL: String) async throws -> [Project] {
        guard let url = URL(string: "\(baseURL)/api/projects") else {
            throw APIError.invalidURL
        }
        
        let (data, response) = try await session.data(from: url)
        
        guard let httpResponse = response as? HTTPURLResponse,
              httpResponse.statusCode == 200 else {
            throw APIError.httpError(statusCode: (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
        
        let projectsResponse = try JSONDecoder().decode(ProjectsResponse.self, from: data)
        return projectsResponse.projects
    }
    
    func fetchConversationHistories(baseURL: String, encodedProjectName: String) async throws -> [ConversationSummary] {
        guard let url = URL(string: "\(baseURL)/api/projects/\(encodedProjectName)/histories") else {
            throw APIError.invalidURL
        }
        
        let (data, response) = try await session.data(from: url)
        
        guard let httpResponse = response as? HTTPURLResponse,
              httpResponse.statusCode == 200 else {
            throw APIError.httpError(statusCode: (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
        
        let historyResponse = try JSONDecoder().decode(HistoryListResponse.self, from: data)
        return historyResponse.conversations
    }
    
    func fetchConversationHistory(baseURL: String, encodedProjectName: String, sessionId: String) async throws -> ConversationHistory {
        guard let url = URL(string: "\(baseURL)/api/projects/\(encodedProjectName)/histories/\(sessionId)") else {
            throw APIError.invalidURL
        }
        
        let (data, response) = try await session.data(from: url)
        
        guard let httpResponse = response as? HTTPURLResponse,
              httpResponse.statusCode == 200 else {
            throw APIError.httpError(statusCode: (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
        
        return try JSONDecoder().decode(ConversationHistory.self, from: data)
    }
    
    func abortRequest(baseURL: String, requestId: String) async throws {
        guard let url = URL(string: "\(baseURL)/api/abort/\(requestId)") else {
            throw APIError.invalidURL
        }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        
        let (_, response) = try await session.data(for: request)
        
        guard let httpResponse = response as? HTTPURLResponse,
              httpResponse.statusCode == 200 else {
            throw APIError.httpError(statusCode: (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
    }
}

enum APIError: LocalizedError {
    case invalidURL
    case httpError(statusCode: Int)
    case decodingError
    case networkError
    
    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "Invalid URL"
        case .httpError(let statusCode):
            return "HTTP Error: \(statusCode)"
        case .decodingError:
            return "Failed to decode response"
        case .networkError:
            return "Network error occurred"
        }
    }
}