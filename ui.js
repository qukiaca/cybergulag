document.addEventListener("DOMContentLoaded", () => {
    const body = document.body;
    const themeBtn = document.getElementById("theme-toggle");
    const container = document.querySelector(".app-container");
    const backBtn = document.getElementById("back-to-contacts");
    const closeBtn = document.getElementById("close-chat");
    const msgInput = document.getElementById("msgInput");
    const sendBtn = document.getElementById("sendBtn");
    const recordBtn = document.getElementById("recordBtn");

    // --- Темы ---
    const updateThemeIcon = (isDark) => {
        themeBtn.innerHTML = `<img src="./assets/${isDark ? 'sun.svg' : 'moon.svg'}" alt="Тема">`;
    };

    themeBtn.addEventListener("click", () => {
        body.classList.toggle("dark-theme");
        const isDark = body.classList.contains("dark-theme");
        localStorage.setItem("p2p-theme", isDark ? "dark" : "light");
        updateThemeIcon(isDark);
    });

    // Инициализация темы
    const savedTheme = localStorage.getItem("p2p-theme") || "dark";
    if (savedTheme === "light") body.classList.remove("dark-theme");
    updateThemeIcon(body.classList.contains("dark-theme"));

    // --- Мобильная навигация ---
    const isMobile = () => window.innerWidth <= 768;

    window.ui = {
        openChat: (id) => {
            container.classList.add("chat-open");
            document.getElementById("welcome-screen").classList.add("hidden");
            document.getElementById("chat-view").classList.remove("hidden");
            document.getElementById("activeContactId").textContent = id;
        },
        closeChat: () => {
            container.classList.remove("chat-open");
            document.getElementById("chat-view").classList.add("hidden");
            document.getElementById("welcome-screen").classList.remove("hidden");
            document.querySelectorAll("#contactList li").forEach(el => el.classList.remove("active"));
            if (typeof targetPeer !== "undefined") {
                targetPeer = null;
            }
        }
    };

    // Back button - mobile only (prevent browser history)
    backBtn?.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        window.ui.closeChat();
    });

    // Close button - works on both mobile and desktop
    closeBtn?.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        window.ui.closeChat();
    });

    // ESC key - close chat on desktop
    document.addEventListener("keydown", (e) => {
        if (e.key === "Escape" && container.classList.contains("chat-open")) {
            window.ui.closeChat();
        }
    });

    // --- Кнопки ввода ---
    msgInput.addEventListener("input", () => {
        if (msgInput.value.trim().length > 0) {
            sendBtn.classList.remove("hidden");
            recordBtn.classList.add("hidden");
        } else {
            sendBtn.classList.add("hidden");
            recordBtn.classList.remove("hidden");
        }
    });
});