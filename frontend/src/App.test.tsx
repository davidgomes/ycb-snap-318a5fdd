import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { ProjectSelector } from "./components/ProjectSelector";
import { AgentHubPage } from "./components/native/AgentHubPage";
import { EnterBehaviorProvider } from "./contexts/EnterBehaviorContext";

// Mock fetch globally
global.fetch = vi.fn();

describe("App Routing", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Mock projects API response
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ projects: [] }),
    });
  });

  it("renders project selection page at root path", async () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/" element={<ProjectSelector />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText("Select a Project")).toBeInTheDocument();
    });
  });

  it("renders agent hub page when navigating to projects path", () => {
    // Mock the agent config
    vi.mock('./config/agentConfig.json', () => ({
      default: {
        agents: [
          {
            id: "test-agent",
            name: "Test Agent",
            description: "Test Description",
            workingDirectory: "/test",
            apiEndpoint: "http://localhost:8080",
            isOrchestrator: false
          }
        ]
      }
    }));

    render(
      <EnterBehaviorProvider>
        <MemoryRouter initialEntries={["/"]}>
          <Routes>
            <Route path="/" element={<AgentHubPage />} />
          </Routes>
        </MemoryRouter>
      </EnterBehaviorProvider>,
    );

    // Just check that the component renders without error
    expect(screen.getByRole("main")).toBeInTheDocument();
  });

  it("shows new directory selection button", async () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/" element={<ProjectSelector />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText("Select New Directory")).toBeInTheDocument();
    });
  });
});
