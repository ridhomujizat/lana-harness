import { createRoot } from "react-dom/client";
import "./styles/index.css";
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
