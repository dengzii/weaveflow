const header = document.querySelector("[data-header]");
const menuButton = document.querySelector("[data-menu-button]");
const menu = document.querySelector("[data-menu]");
const copyButton = document.querySelector("[data-copy-button]");
const code = document.querySelector("[data-code]");
const year = document.querySelector("[data-year]");

const updateHeader = () => {
  header?.classList.toggle("scrolled", window.scrollY > 12);
};

updateHeader();
window.addEventListener("scroll", updateHeader, { passive: true });

menuButton?.addEventListener("click", () => {
  const isOpen = menuButton.getAttribute("aria-expanded") === "true";
  menuButton.setAttribute("aria-expanded", String(!isOpen));
  menu?.classList.toggle("open", !isOpen);
});

menu?.querySelectorAll("a").forEach((link) => {
  link.addEventListener("click", () => {
    menuButton?.setAttribute("aria-expanded", "false");
    menu.classList.remove("open");
  });
});

copyButton?.addEventListener("click", async () => {
  if (!code) {
    return;
  }

  try {
    await navigator.clipboard.writeText(code.textContent ?? "");
    const label = copyButton.querySelector("span");
    if (label) {
      label.textContent = "Copied";
      window.setTimeout(() => {
        label.textContent = "Copy";
      }, 1600);
    }
  } catch {
    copyButton.querySelector("span")?.replaceChildren("Select code");
  }
});

const revealObserver = new IntersectionObserver(
  (entries) => {
    entries.forEach((entry) => {
      if (entry.isIntersecting) {
        entry.target.classList.add("visible");
        revealObserver.unobserve(entry.target);
      }
    });
  },
  { threshold: 0.12 },
);

document.querySelectorAll(".reveal").forEach((element) => revealObserver.observe(element));

if (year) {
  year.textContent = String(new Date().getFullYear());
}
