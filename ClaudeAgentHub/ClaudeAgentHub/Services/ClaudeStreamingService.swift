import Foundation
import Combine

class ClaudeStreamingService: ObservableObject {
    private var streamingTask: URLSessionDataTask?
    private let session = URLSession.shared
    
    func sendMessage(
        message: String,
        sessionId: String?,
        requestId: String,
        workingDirectory: String?,
        availableAgents: [Agent],
        baseURL: String,
        onStreamUpdate: @escaping (StreamResponse) -> Void,
        onCompletion: @escaping (Result<Void, Error>) -> Void
    ) {
        guard let url = URL(string: "\(baseURL)/api/chat") else {
            onCompletion(.failure(APIError.invalidURL))
            return
        }
        
        let chatRequest = ChatRequest(
            message: message,
            sessionId: sessionId,
            requestId: requestId,
            workingDirectory: workingDirectory,
            availableAgents: availableAgents.map { agent in
                ChatRequest.AgentInfo(
                    id: agent.id,
                    name: agent.name,
                    description: agent.description,
                    workingDirectory: agent.workingDirectory,
                    apiEndpoint: agent.apiEndpoint,
                    isOrchestrator: agent.isOrchestrator
                )
            }
        )
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        
        do {
            request.httpBody = try JSONEncoder().encode(chatRequest)
        } catch {
            onCompletion(.failure(error))
            return
        }
        
        streamingTask = session.dataTask(with: request) { [weak self] data, response, error in
            if let error = error {
                DispatchQueue.main.async {
                    onCompletion(.failure(error))
                }
                return
            }
            
            guard let httpResponse = response as? HTTPURLResponse else {
                DispatchQueue.main.async {
                    onCompletion(.failure(APIError.networkError))
                }
                return
            }
            
            guard httpResponse.statusCode == 200 else {
                DispatchQueue.main.async {
                    onCompletion(.failure(APIError.httpError(statusCode: httpResponse.statusCode)))
                }
                return
            }
            
            guard let data = data else {
                DispatchQueue.main.async {
                    onCompletion(.failure(APIError.networkError))
                }
                return
            }
            
            self?.processStreamData(data, onStreamUpdate: onStreamUpdate, onCompletion: onCompletion)
        }
        
        streamingTask?.resume()
    }
    
    private func processStreamData(
        _ data: Data,
        onStreamUpdate: @escaping (StreamResponse) -> Void,
        onCompletion: @escaping (Result<Void, Error>) -> Void
    ) {
        let dataString = String(data: data, encoding: .utf8) ?? ""
        let lines = dataString.components(separatedBy: .newlines)
        
        for line in lines {
            let trimmedLine = line.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmedLine.isEmpty {
                do {
                    let streamResponse = try JSONDecoder().decode(StreamResponse.self, from: trimmedLine.data(using: .utf8) ?? Data())
                    
                    DispatchQueue.main.async {
                        onStreamUpdate(streamResponse)
                        
                        // Check if this is a completion signal
                        if streamResponse.type == "done" || streamResponse.type == "aborted" {
                            onCompletion(.success(()))
                        }
                    }
                } catch {
                    // Continue processing other lines even if one fails to decode
                    print("Failed to decode line: \(trimmedLine), error: \(error)")
                }
            }
        }
    }
    
    func cancelCurrentRequest() {
        streamingTask?.cancel()
        streamingTask = nil
    }
}

// MARK: - Message Processing Extensions
extension ClaudeStreamingService {
    func processStreamResponse(_ response: StreamResponse) -> ChatMessage? {
        guard response.type == "claude_json",
              let data = response.data else {
            return nil
        }
        
        let timestamp = Date()
        let messageId = UUID().uuidString
        
        switch data.type {
        case "system":
            return ChatMessage(
                id: messageId,
                type: .system,
                role: .system,
                content: "System initialized - Working directory: \(data.cwd ?? "Unknown")",
                timestamp: timestamp
            )
            
        case "assistant":
            if let messageContent = data.message,
               let content = messageContent.content {
                
                var textContent = ""
                var toolName: String?
                var toolInput: [String: String]?
                
                for item in content {
                    switch item.type {
                    case "text":
                        if let text = item.text {
                            textContent += text
                        }
                    case "tool_use":
                        toolName = item.name
                        toolInput = item.input?.compactMapValues { $0.value as? String }
                    default:
                        break
                    }
                }
                
                if let toolName = toolName {
                    return ChatMessage(
                        id: messageId,
                        type: .tool,
                        role: .assistant,
                        content: textContent,
                        timestamp: timestamp,
                        toolName: toolName,
                        toolInput: toolInput
                    )
                } else {
                    return ChatMessage(
                        id: messageId,
                        type: .assistant,
                        role: .assistant,
                        content: textContent,
                        timestamp: timestamp
                    )
                }
            }
            
        case "result":
            return ChatMessage(
                id: messageId,
                type: .system,
                role: .system,
                content: "Task completed",
                timestamp: timestamp
            )
            
        default:
            break
        }
        
        return nil
    }
    
    func extractSessionId(from response: StreamResponse) -> String? {
        return response.data?.sessionId
    }
}