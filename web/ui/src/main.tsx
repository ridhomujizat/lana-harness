import { createRoot } from "react-dom/client";
import "./styles/tokens.css";
import "./styles/app.css";
import "./styles/setup.css";
import "./styles/agent-tabs.css";
import "./styles/skills.css";
import "./styles/mcp.css";
import "./styles/channels.css";
import "./styles/settings.css";
import "./styles/team.css";
import "./styles/rooms.css";
import "./styles/question.css";
import "./styles/markdown.css";
import { App } from "./app/App";
import { AnnouncerProvider } from "./ui/ui";

const mount = document.getElementById("app");
if (mount) {
  /* React replaces the container content, so drop the static loading copy
     before the first render. */
  mount.textContent = "";
  createRoot(mount).render(
    <AnnouncerProvider>
      <App />
    </AnnouncerProvider>
  );
}
