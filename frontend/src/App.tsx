import { HashRouter as Router, Routes, Route } from "react-router-dom";
import { AgentHubPage } from "./components/native/AgentHubPage";
import { EnterBehaviorProvider } from "./contexts/EnterBehaviorContext";

function App() {
  return (
    <EnterBehaviorProvider>
      <Router>
        <Routes>
          <Route path="/" element={<AgentHubPage />} />
        </Routes>
      </Router>
    </EnterBehaviorProvider>
  );
}

export default App;
