document.addEventListener("DOMContentLoaded", () => {
    const body = document.body;
    const themeBtn = document.getElementById("theme-toggle");
    const container = document.querySelector(".app-container");
    const backBtn = document.getElementById("mobile-back");
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
    window.ui = {
        openChat: (id) => {
            container.classList.add("chat-open");
            document.getElementById("welcome-screen").classList.add("hidden");
            document.getElementById("chat-view").classList.remove("hidden");
            document.getElementById("activeContactId").textContent = id;
        },
        closeChat: () => {
            container.classList.remove("chat-open");
        }
    };

    backBtn.addEventListener("click", () => window.ui.closeChat());

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