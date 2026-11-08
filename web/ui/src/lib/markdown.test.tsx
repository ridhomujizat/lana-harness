import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Markdown } from "./markdown";

describe("markdown", () => {
  it("renders headings and paragraphs", () => {
    render(<Markdown text={"# Title\n\nA paragraph."} />);
    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.textContent).toBe("Title");
    expect(screen.getByText("A paragraph.")).toBeTruthy();
  });

  it("renders emphasis and inline code", () => {
    render(<Markdown text={"Some **bold** and `code`."} />);
    const bold = document.querySelector("strong.md-strong");
    expect(bold?.textContent).toBe("bold");
    const code = document.querySelector("code.md-inline-code");
    expect(code?.textContent).toBe("code");
  });

  it("renders safe links and leaves unsafe schemes as text", () => {
    render(
      <Markdown
        text={"[lana](https://example.com) and [bad](javascript:alert(1))"}
      />
    );
    const link = screen.getByRole("link", { name: "lana" });
    expect(link.getAttribute("href")).toBe("https://example.com");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(screen.queryByRole("link", { name: "bad" })).toBeNull();
    expect(screen.getByText(/bad/)).toBeTruthy();
  });

  it("renders fenced code with a copy button and language", () => {
    render(<Markdown text={"```bash\nlana daemon\n```"} />);
    expect(document.querySelector(".md-code-lang")?.textContent).toBe("bash");
    expect(document.querySelector(".md-code-text")?.textContent).toBe(
      "lana daemon"
    );
    expect(screen.getByRole("button", { name: "Copy code" })).toBeTruthy();
  });

  it("omits the header row for a block without a language", () => {
    render(<Markdown text={"```\nplain text\n```"} />);
    expect(document.querySelector(".md-code-head")).toBeNull();
    expect(document.querySelector(".md-code-text")?.textContent).toBe(
      "plain text"
    );
    expect(screen.getByRole("button", { name: "Copy code" })).toBeTruthy();
  });

  it("renders a table", () => {
    render(<Markdown text={"| a | b |\n| --- | --- |\n| 1 | 2 |"} />);
    expect(document.querySelectorAll("table.md-table th")).toHaveLength(2);
    expect(document.querySelectorAll("table.md-table td")).toHaveLength(2);
  });
});
