(() => {
  const themeStorageKey = "operator-theme";
  const root = document.documentElement;
  const themeToggle = document.querySelector("[data-theme-toggle]");
  const sidebar = document.querySelector("[data-sidebar]");
  const toggle = document.querySelector("[data-sidebar-toggle]");
  const backdrop = document.querySelector("[data-sidebar-backdrop]");
  const copyButtons = Array.from(document.querySelectorAll("[data-copy-value]"));

  const syncThemeToggle = () => {
    if (!themeToggle) {
      return;
    }
    const light = root.dataset.theme === "light";
    themeToggle.textContent = light ? "Dark mode" : "Light mode";
    themeToggle.setAttribute("aria-pressed", light ? "true" : "false");
  };

  const setTheme = (theme) => {
    if (theme === "light") {
      root.dataset.theme = "light";
      localStorage.setItem(themeStorageKey, "light");
    } else {
      delete root.dataset.theme;
      localStorage.setItem(themeStorageKey, "dark");
    }
    syncThemeToggle();
  };

  if (themeToggle) {
    syncThemeToggle();
    themeToggle.addEventListener("click", () => {
      setTheme(root.dataset.theme === "light" ? "dark" : "light");
    });
  }

  if (!sidebar || !toggle || !backdrop) {
    // Keep copy helpers active even on auth pages where the shell is absent.
    copyButtons.forEach((button) => {
      button.addEventListener("click", async () => {
        const original = button.textContent;
        try {
          await navigator.clipboard.writeText(button.dataset.copyValue || "");
          button.textContent = "Copied";
          button.classList.add("copied");
          window.setTimeout(() => {
            button.textContent = original;
            button.classList.remove("copied");
          }, 1200);
        } catch (_) {}
      });
    });
    return;
  }

  const setOpen = (open) => {
    sidebar.classList.toggle("is-open", open);
    backdrop.hidden = !open;
  };

  toggle.addEventListener("click", () => {
    setOpen(!sidebar.classList.contains("is-open"));
  });

  backdrop.addEventListener("click", () => {
    setOpen(false);
  });

  window.addEventListener("resize", () => {
    if (window.innerWidth > 860) {
      setOpen(false);
    }
  });

  copyButtons.forEach((button) => {
    button.addEventListener("click", async () => {
      const original = button.textContent;
      try {
        await navigator.clipboard.writeText(button.dataset.copyValue || "");
        button.textContent = "Copied";
        button.classList.add("copied");
        window.setTimeout(() => {
          button.textContent = original;
          button.classList.remove("copied");
        }, 1200);
      } catch (_) {}
    });
  });
})();
