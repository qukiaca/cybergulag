// Create and show login modal
function createAuthModal() {
    // Remove existing modal if any
    const existingModal = document.getElementById('auth-modal');
    if (existingModal) {
        existingModal.remove();
    }
    
    // Create modal backdrop
    const modalBackdrop = document.createElement('div');
    modalBackdrop.id = 'auth-modal-backdrop';
    modalBackdrop.style.position = 'fixed';
    modalBackdrop.style.top = '0';
    modalBackdrop.style.left = '0';
    modalBackdrop.style.width = '100%';
    modalBackdrop.style.height = '100%';
    modalBackdrop.style.backgroundColor = 'rgba(0,0,0,0.5)';
    modalBackdrop.style.display = 'flex';
    modalBackdrop.style.alignItems = 'center';
    modalBackdrop.style.justifyContent = 'center';
    modalBackdrop.style.zIndex = '1000';
    
    // Create modal container
    const modalContainer = document.createElement('div');
    modalContainer.style.backgroundColor = '#1e1e1e';
    modalContainer.style.color = '#fff';
    modalContainer.style.padding = '2rem';
    modalContainer.style.borderRadius = '8px';
    modalContainer.style.width = '90%';
    modalContainer.style.maxWidth = '400px';
    modalContainer.style.boxShadow = '0 4px 6px rgba(0,0,0,0.1)';
    
    // Create modal content
    modalContainer.innerHTML = `
        <h2 style="margin-top: 0; text-align: center;">Pear2P</h2>
        <div id="auth-error" style="color: #ff6b6b; margin-bottom: 1rem; min-height: 1.5rem; text-align: center;"></div>
        <div style="margin-bottom: 1rem;">
            <label style="display: block; margin-bottom: 0.5rem; font-size: 0.9rem;">Логин</label>
            <input type="text" id="auth-username" style="width: 100%; padding: 0.75rem; border: 1px solid #444; border-radius: 4px; background: #2d2d2d; color: #fff; font-size: 1rem;">
        </div>
        <div style="margin-bottom: 1rem;">
            <label style="display: block; margin-bottom: 0.5rem; font-size: 0.9rem;">Пароль</label>
            <input type="password" id="auth-password" style="width: 100%; padding: 0.75rem; border: 1px solid #444; border-radius: 4px; background: #2d2d2d; color: #fff; font-size: 1rem;">
        </div>
        <div style="display: flex; gap: 1rem;">
            <button id="auth-login" style="flex: 1; padding: 0.75rem; background: #4f46e5; color: white; border: none; border-radius: 4px; font-size: 1rem; cursor: pointer;">Войти</button>
            <button id="auth-register" style="flex: 1; padding: 0.75rem; background: #10b981; color: white; border: none; border-radius: 4px; font-size: 1rem; cursor: pointer;">Зарегистрироваться</button>
        </div>
    `;
    
    modalBackdrop.appendChild(modalContainer);
    document.body.appendChild(modalBackdrop);
    
    return {
        backdrop: modalBackdrop,
        usernameInput: modalContainer.querySelector('#auth-username'),
        passwordInput: modalContainer.querySelector('#auth-password'),
        errorElement: modalContainer.querySelector('#auth-error'),
        loginBtn: modalContainer.querySelector('#auth-login'),
        registerBtn: modalContainer.querySelector('#auth-register')
    };
}

// Authentication flow with modal
async function authenticateUser() {
    const storedUsername = localStorage.getItem('pear2p-username');
    if (storedUsername) {
        return storedUsername;
    }
    
    // Show modal
    const modal = createAuthModal();
    modal.backdrop.style.display = 'flex';
    
    // Return a promise that resolves when authentication succeeds
    return new Promise((resolve, reject) => {
        const attemptAuth = async (isRegister = false) => {
            const username = modal.usernameInput.value.trim();
            const password = modal.passwordInput.value.trim();
            
            if (!username || !password) {
                modal.errorElement.textContent = 'Введите логин и пароль';
                return;
            }
            
            modal.errorElement.textContent = '';
            modal.loginBtn.disabled = true;
            modal.registerBtn.disabled = true;
            
            try {
                const endpoint = isRegister ? '/api/register' : '/api/login';
                const response = await fetch(endpoint, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username, password })
                });
                
                if (!response.ok) {
                    const errorData = await response.json();
                    let errorMessage = 'Ошибка аутентификации';
                    if (response.status === 401) {
                        errorMessage = 'Неверный логин или пароль';
                    } else if (response.status === 409) {
                        errorMessage = 'Пользователь уже существует';
                    }
                    modal.errorElement.textContent = errorMessage;
                    modal.loginBtn.disabled = false;
                    modal.registerBtn.disabled = false;
                    return;
                }
                
                const data = await response.json();
                localStorage.setItem('pear2p-username', data.username);
                
                // Hide modal
                modal.backdrop.style.display = 'none';
                
                resolve(data.username);
            } catch (err) {
                console.error('Auth error:', err);
                modal.errorElement.textContent = 'Ошибка соединения с сервером';
                modal.loginBtn.disabled = false;
                modal.registerBtn.disabled = false;
            }
        };
        
        modal.loginBtn.addEventListener('click', () => attemptAuth(false));
        modal.registerBtn.addEventListener('click', () => attemptAuth(true));
        
        // Allow Enter key to submit
        modal.usernameInput.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') attemptAuth(false);
        });
        modal.passwordInput.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') attemptAuth(false);
        });
    });
}

// Initialize authentication and WebSocket connection
async function initializeApp() {
    try {
        myId = await authenticateUser();
        
        // Set username in UI
        document.getElementById("myId").textContent = myId;
        
        // Initialize WebSocket connection
        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        window.ws = new WebSocket(`${protocol}//${location.host}/ws?user_id=${myId}`);
        
        ws.onmessage = (e) => {
            const msg = JSON.parse(e.data);
            if (msg.type === "contacts") renderContacts(msg.payload);
            if (msg.type === "chat") displayMessage(msg.from, msg.payload, msg.mediaType);
        };
    } catch (err) {
        console.error('Failed to initialize app:', err);
        throw err;
    }
}

// Start the app
initializeApp().catch(err => {
    console.error('Application failed to start:', err);
    alert('Не удалось запустить приложение. Перезагрузите страницу.');
});
let targetPeer = null;

let mediaMode = 'voice';
let isRecording = false;
let mediaRecorder = null;
let chunks = [];
let pressTimer = null;
let localStream = null;

const chatEl = document.getElementById("chat-messages");
const msgInput = document.getElementById("msgInput");
const recordBtn = document.getElementById("recordBtn");
const videoPreview = document.getElementById("videoPreview");
const contactList = document.getElementById("contactList");

function displayMessage(from, payload, type = 'text') {
    const div = document.createElement("div");
    div.className = `msg ${from === myId ? 'mine' : ''}`;
    
    const displayName = from === myId ? "Вы" : from;

    // Создаём имя безопасно — через textContent
    const nameEl = document.createElement("strong");
    nameEl.textContent = displayName;
    nameEl.style.display = "block";
    nameEl.style.fontSize = "0.7rem";
    nameEl.style.opacity = "0.8";
    nameEl.style.marginBottom = "4px";
    div.appendChild(nameEl);

    // Обрабатываем контент в зависимости от типа
    if (type === 'text') {
        // 🔒 Текст — ТОЛЬКО через textContent, никаких innerHTML
        const textEl = document.createElement("span");
        textEl.textContent = payload;
        textEl.style.whiteSpace = "pre-wrap"; // сохраняем переносы строк
        div.appendChild(textEl);
        
    } else if (type === 'voice') {
        // Base64 URL для audio src — безопасно, браузер сам экранирует
        const audio = document.createElement("audio");
        audio.src = payload;
        audio.controls = true;
        audio.style.maxWidth = "100%";
        audio.style.marginTop = "8px";
        div.appendChild(audio);
        
    } else if (type === 'video') {
        const video = document.createElement("video");
        video.src = payload;
        video.className = "video-circle";
        video.loop = true;
        video.playsInline = true;
        video.onclick = () => video.paused ? video.play() : video.pause();
        video.style.marginTop = "8px";
        div.appendChild(video);
    }

    chatEl.appendChild(div);
    chatEl.scrollTop = chatEl.scrollHeight;
}

// Запись
recordBtn.onclick = () => {
    if (isRecording) { stopAndSend(); return; }
    mediaMode = (mediaMode === 'voice') ? 'video' : 'voice';
    document.getElementById("mic-icon").classList.toggle("hidden", mediaMode !== 'voice');
    document.getElementById("video-icon").classList.toggle("hidden", mediaMode !== 'video');
};

recordBtn.onmousedown = recordBtn.ontouchstart = (e) => {
    if (isRecording) return;
    pressTimer = setTimeout(startRecording, 1500);
};

recordBtn.onmouseup = recordBtn.onmouseleave = recordBtn.ontouchend = () => {
    clearTimeout(pressTimer);
};

async function startRecording() {
    if (!targetPeer) return alert("Выбери контакт!");
    isRecording = true;
    recordBtn.classList.add("recording");
    chunks = [];

    const constraints = {
        audio: true,
        video: mediaMode === 'video' ? { width: 400, height: 400 } : false
    };

    try {
        localStream = await navigator.mediaDevices.getUserMedia(constraints);
        if (mediaMode === 'video') {
            videoPreview.style.display = "block";
            videoPreview.srcObject = localStream;
        }
        mediaRecorder = new MediaRecorder(localStream);
        mediaRecorder.ondataavailable = (e) => chunks.push(e.data);
        mediaRecorder.onstop = sendMedia;
        mediaRecorder.start();
    } catch (err) { cleanupRecord(); }
}

function stopAndSend() {
    if (mediaRecorder && isRecording) {
        mediaRecorder.stop();
        cleanupRecord();
    }
}

function cleanupRecord() {
    isRecording = false;
    recordBtn.classList.remove("recording");
    videoPreview.style.display = "none";
    if (localStream) localStream.getTracks().forEach(t => t.stop());
}

function sendMedia() {
    const blob = new Blob(chunks, { type: mediaMode === 'voice' ? 'audio/ogg' : 'video/webm' });
    const reader = new FileReader();
    reader.onloadend = () => {
        const base64 = reader.result;
        ws.send(JSON.stringify({ type: "chat", to: targetPeer, payload: base64, mediaType: mediaMode }));
        displayMessage(myId, base64, mediaMode);
    };
    reader.readAsDataURL(blob);
}

function renderContacts(users) {
    contactList.innerHTML = "";
    users.forEach(id => {
        if (id === myId) return;
        const li = document.createElement("li");
        li.textContent = id;
        li.onclick = () => {
            targetPeer = id;
            document.querySelectorAll("#contactList li").forEach(el => el.classList.remove("active"));
            li.classList.add("active");
            window.ui.openChat(id);
        };
        contactList.appendChild(li);
    });
}

const sendMessage = () => {
    const text = msgInput.value.trim();
    if (text && targetPeer) {
        ws.send(JSON.stringify({ type: "chat", to: targetPeer, payload: text, mediaType: 'text' }));
        displayMessage(myId, text, 'text');
        msgInput.value = "";
        msgInput.dispatchEvent(new Event("input"));
    }
};

// Add event listeners for sending messages
msgInput.addEventListener('keypress', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
    }
});
const sendBtn = document.getElementById('sendBtn');
if (sendBtn) {
    sendBtn.addEventListener('click', sendMessage);
}