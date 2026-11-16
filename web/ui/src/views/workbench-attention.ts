import { useEffect, useState } from "react";

import { listWorkbenchSessions } from "../api/workbench";

/* REQ-WB-050: sessions that need the owner, for the sidebar badge. */
export function useWorkbenchAttention(): number {
  const [count, setCount] = useState(0);
  useEffect(() => {
    let alive = true;
    const load = () => {
      listWorkbenchSessions()
        .then((r) => alive && setCount((r.sessions || []).filter((s) => s.needs_you).length))
        .catch(() => alive && setCount(0));
    };
    load();
    const timer = setInterval(load, 20000);
    window.addEventListener("focus", load);
    return () => {
      alive = false;
      clearInterval(timer);
      window.removeEventListener("focus", load);
    };
  }, []);
  return count;
}
