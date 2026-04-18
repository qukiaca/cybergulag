/**
 * 📦 pear2p frontend — логика диалогов
 * 
 * Ключевые принципы:
 * 1. Диалог определяется парой участников: getDialogKey(a, b) = [a,b].sort().join('|')
 * 2. Сообщения отображаются только если принадлежат текущему диалогу (myId + targetPeer)
 * 3. Все остальные сообщения буферизируются по dialogKey
 * 4. При смене диалога: очистка чата → загрузка буфера → отображение
 * 
 * Поток данных:
 * WebSocket → onmessage → displayMessage() → (показать/забуферизировать)
 * Клик по контакту → установить targetPeer → загрузить буфер → очистить буфер
 */

// ============================================================================
// КОНСТАНТЫ
// ============================================================================
const DIALOG_BUFFER_LIMIT = 200;     // Максимум сообщений в буфере одного диалога
const MAX_MESSAGE_QUEUE = 50;        // Максимум сообщений в очереди отправки
const MEDIA_SIZE_LIMIT_KB = 500;     // Лимит размера медиа в KB

// ============================================================================
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
    console.log('🎯 pear2p frontend loaded, dialog logic: strict mode');
    
    try {
        myId = await authenticateUser();
        
        // Set username in UI
        document.getElementById("myId").textContent = myId;
        
        // Initialize WebSocket connection with reconnection logic
        await connectWebSocket();
        
        // Fetch all users for contact list
        fetch('/api/users')
            .then(r => r.json())
            .then(data => {
                if (Array.isArray(data.users)) {
                    allUsers = data.users;
                    // Optionally mark online users if they're already in the list
                    data.users.forEach(u => {
                        if (onlineUsers.has(u)) onlineUsers.add(u);
                    });
                    renderContactList();
                    // adjustChatContainer(); // CSS flexbox handles layout
                }
            })
            .catch(err => console.error('Failed to load users:', err));
    } catch (err) {
        console.error('Failed to initialize app:', err);
        throw err;
    }
}

// WebSocket connection with reconnection logic
let reconnectAttempts = 0;
const maxReconnectAttempts = 3;
const reconnectDelay = 2000; // 2 seconds

// Очередь сообщений для отправки при восстановлении соединения
const messageQueue = [];

// Обновление визуального индикатора статуса
function updateConnectionStatus(connected) {
    const statusEl = document.getElementById('connection-status');
    if (statusEl) {
        statusEl.textContent = connected ? '🟢' : '🔴';
        statusEl.title = connected ? 'Соединение установлено' : 'Нет соединения';
    }
}

function connectWebSocket() {
    return new Promise((resolve, reject) => {
        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        window.ws = new WebSocket(`${protocol}//${location.host}/ws?user_id=${myId}`);
        
        ws.onopen = () => {
            console.log('✅ WebSocket соединение установлено');
            reconnectAttempts = 0;
            updateConnectionStatus(true);
            
            // Отправляем накопленные сообщения и обновляем их статус
            while (messageQueue.length > 0) {
                const queued = messageQueue.shift();
                if (ws.readyState === WebSocket.OPEN) {
                    ws.send(queued);
                    // Обновляем индикатор на сообщении в чате
                    updateQueuedMessageStatus('✓');
                }
            }
            resolve();
        };
        
        ws.onmessage = (e) => {
            try {
                const msg = JSON.parse(e.data);
                
                // Проверяем известные типы сообщений
                const knownTypes = ['contacts', 'chat'];
                if (!knownTypes.includes(msg.type)) {
                    console.warn(`⚠️ Неизвестный тип сообщения: "${msg.type}"`, msg);
                    return;
                }
                
                if (msg.type === "contacts" && Array.isArray(msg.payload)) {
                    console.log(`📡 Получен список онлайн: ${JSON.stringify(msg.payload)}`);
                    onlineUsers.clear();
                    msg.payload.forEach(id => onlineUsers.add(id));
                    renderContactList();
                }
                if (msg.type === "chat") {
                    // Передаём: from, to, payload, mediaType
                    displayMessage(msg.from, msg.to, msg.payload, msg.mediaType);
                }
            } catch (err) {
                console.error('❌ Ошибка парсинга сообщения:', err, 'raw:', e.data);
            }
        };
        
        ws.onerror = (event) => {
            const details = [];
            if (event.code) details.push(`code: ${event.code}`);
            if (event.message) details.push(`message: ${event.message}`);
            if (event.type) details.push(`type: ${event.type}`);
            console.error('❌ WebSocket ошибка:', details.join(', '), event);
            
            // Помечаем сообщения в очереди как неудавшиеся
            updateQueuedMessageStatus('❌', true);
        };
        
        ws.onclose = (event) => {
            console.log(`🔌 WebSocket соединение закрыто (code: ${event.code}, reason: ${event.reason})`);
            updateConnectionStatus(false);
            
            // Attempt to reconnect if we haven't exceeded max attempts
            if (reconnectAttempts < maxReconnectAttempts) {
                reconnectAttempts++;
                console.log(`🔄 Попытка переподключения ${reconnectAttempts}/${maxReconnectAttempts} через ${reconnectDelay/1000}с...`);
                setTimeout(() => {
                    connectWebSocket().then(resolve).catch(reject);
                }, reconnectDelay);
            } else {
                console.error('❌ Превышено максимальное количество попыток переподключения');
                reject(new Error('Не удалось установить WebSocket соединение после нескольких попыток'));
            }
        };
    });
}

// Start the app
initializeApp().catch(err => {
    console.error('Application failed to start:', err);
    alert('Не удалось запустить приложение. Перезагрузите страницу.');
});
let targetPeer = null;
let allUsers = [];
const onlineUsers = new Set();

let mediaMode = 'voice';
let isRecording = false;
let mediaRecorder = null;
let chunks = [];
let pressTimer = null;
let localStream = null;
let recordTimer = null;
let recordingStartTime = 0;

// Remove border/outline from record button
function removeRecordBtnOutline() {
    if (recordBtn) {
        recordBtn.style.border = 'none';
        recordBtn.style.outline = 'none';
        recordBtn.style.boxShadow = 'none';
    }
    if (videoPreview) {
        videoPreview.style.border = 'none';
        videoPreview.style.outline = 'none';
    }
}

// Call on initialization
document.addEventListener('DOMContentLoaded', removeRecordBtnOutline);

// Also call when entering/exiting recording state
const originalStartRecording = startRecording;
function startRecording() {
    originalStartRecording();
    // Keep outline removed during recording
    removeRecordBtnOutline();
}

const originalCleanupRecord = cleanupRecord;
function cleanupRecord() {
    originalCleanupRecord();
    // Ensure outline stays removed after recording
    removeRecordBtnOutline();
}

const chatEl = document.getElementById("chat-messages");
const msgInput = document.getElementById("msgInput");
const recordBtn = document.getElementById("recordBtn");
const videoPreview = document.getElementById("videoPreview");
const contactList = document.getElementById("contactList");

// Буфер для хранения сообщений по диалогам (ключ = dialogKey)
const messageBuffers = {};

/**
 * Возвращает уникальный ключ диалога между двумя пользователями.
 * Порядок ID не важен — сортировка гарантирует одинаковый ключ для обоих участников.
 * @param {string} id1 - Первый участник диалога
 * @param {string} id2 - Второй участник диалога
 * @returns {string} Ключ вида "alice|bob" (отсортированный)
 * 
 * @example
 * getDialogKey('bob', 'alice') === getDialogKey('alice', 'bob') // true
 */
function getDialogKey(id1, id2) {
    return [id1, id2].sort().join('|');
}

/**
 * Безопасно добавляет сообщение в буфер диалога с защитой от спама.
 * @param {string} dialogKey - Ключ диалога (получен через getDialogKey)
 * @param {string} from - Отправитель сообщения
 * @param {string} to - Получатель сообщения
 * @param {string} payload - Содержимое сообщения (текст или base64)
 * @param {'text'|'voice'|'video'} type - Тип сообщения
 */
function bufferMessage(dialogKey, from, to, payload, type) {
    if (!messageBuffers[dialogKey]) {
        messageBuffers[dialogKey] = [];
    }
    messageBuffers[dialogKey].push({ from, to, payload, type });
    
    // Защита от спама: удаляем старые сообщения, если буфер превысил лимит
    if (messageBuffers[dialogKey].length > DIALOG_BUFFER_LIMIT) {
        messageBuffers[dialogKey] = messageBuffers[dialogKey].slice(-DIALOG_BUFFER_LIMIT);
    }
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
    // Если нажали, но не начали запись (менее 1.5с), показать уведомление
    if (!isRecording) {
        showToast("Слишком коротко");
    }
};

async function startRecording() {
    if (!targetPeer) return alert("Выбери контакт!");
    isRecording = true;
    recordBtn.classList.add("recording");
    chunks = [];
    recordingStartTime = Date.now();
    document.getElementById('record-timer').classList.remove('hidden');
    updateTimer();

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
        
        // Параметры MediaRecorder со сжатием для видео
        const recorderOptions = {};
        if (mediaMode === 'video') {
            // VP8 codec, 256 Kbps — компромисс между качеством и размером
            recorderOptions.mimeType = 'video/webm;codecs=vp8';
            recorderOptions.videoBitsPerSecond = 256000;
        }
        
        mediaRecorder = new MediaRecorder(localStream, recorderOptions);
        mediaRecorder.ondataavailable = (e) => chunks.push(e.data);
        mediaRecorder.onstop = sendMedia;
        mediaRecorder.start();
    } catch (err) {
        // Очищаем ресурсы даже если stream был получен до ошибки
        cleanupRecord();
    }
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
    if (localStream) {
        localStream.getTracks().forEach(t => t.stop());
        console.log('🧹 Очищены медиа-треки');
        localStream = null;
    }
    if (recordTimer) {
        clearTimeout(recordTimer);
        recordTimer = null;
    }
    document.getElementById('record-timer').classList.add('hidden');
    document.getElementById('record-timer').textContent = '00:00';
}

function updateTimer() {
    if (recordTimer) {
        clearTimeout(recordTimer);
    }
    
    const timerElement = document.getElementById('record-timer');
    if (!isRecording) return;
    
    const elapsedSeconds = Math.floor((Date.now() - recordingStartTime) / 1000);
    const minutes = String(Math.floor(elapsedSeconds / 60)).padStart(2, '0');
    const seconds = String(elapsedSeconds % 60).padStart(2, '0');
    timerElement.textContent = `${minutes}:${seconds}`;
    
    recordTimer = setTimeout(updateTimer, 1000);
}

function showToast(message) {
    const toast = document.getElementById('record-toast');
    toast.textContent = message;
    toast.classList.add('show');
    
    // Hide after 1.5 seconds
    setTimeout(() => {
        toast.classList.remove('show');
    }, 1500);
}

function sendMedia() {
    const blob = new Blob(chunks, { type: mediaMode === 'voice' ? 'audio/ogg' : 'video/webm' });
    
    // Проверка размера: если blob > MEDIA_SIZE_LIMIT_KB — предупреждение
    const SIZE_LIMIT = MEDIA_SIZE_LIMIT_KB * 1024;
    if (blob.size > SIZE_LIMIT) {
        showToast("Слишком большое видео, попробуйте короче");
        console.warn(`⚠️ Blob размером ${(blob.size / 1024).toFixed(1)} KB превышает лимит ${MEDIA_SIZE_LIMIT_KB} KB`);
        return;
    }
    
    // Прогресс-индикатор
    const timerEl = document.getElementById('record-timer');
    const originalText = timerEl.textContent;
    timerEl.textContent = '⏳';
    
    let progressUpdated = false;
    const progressStarted = Date.now();
    
    // Фолбэк: если onprogress не сработал за 500ms, показываем ⏳
    const progressFallback = setInterval(() => {
        if (Date.now() - progressStarted > 500 && !progressUpdated) {
            timerEl.textContent = '⏳';
        }
    }, 200);
    
    const reader = new FileReader();
    reader.onprogress = (e) => {
        if (e.lengthComputable) {
            progressUpdated = true;
            const percent = Math.round((e.loaded / e.total) * 100);
            timerEl.textContent = `${percent}%`;
        }
    };
    reader.onloadend = () => {
        clearInterval(progressFallback);
        timerEl.textContent = originalText;
        const base64 = reader.result;
        console.log(`📊 Размер base64 данных: ${(base64.length / 1024).toFixed(1)} KB`);
        
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: "chat", to: targetPeer, payload: base64, mediaType: mediaMode }));
            // Передаём: from=myId, to=targetPeer
            displayMessage(myId, targetPeer, base64, mediaMode);
        } else {
            console.error('❌ WebSocket не готов к отправке медиа');
            showToast('Ошибка отправки');
        }
    };
    reader.readAsDataURL(blob);
}

/**
 * Отрисовывает список контактов из allUsers.
 * Для каждого контакта:
 * - Класс "online"/"offline" по статусу в onlineUsers
 * - Обработчик onclick: смена диалога (очистка → загрузка истории → установка targetPeer)
 */

/**
 * Загружает историю сообщений с сервера для указанного пользователя.
 * @param {string} targetUser - Пользователь, с которым ведётся диалог
 * @returns {Promise<Array>} Массив сообщений
 */
async function loadHistory(targetUser) {
    try {
        const response = await fetch(`/api/history?with=${encodeURIComponent(targetUser)}`, {
            headers: {
                'X-User-ID': myId
            }
        });
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}`);
        }
        
        const data = await response.json();
        console.log(`📥 Загружено ${data.messages?.length || 0} сообщений с сервера для ${targetUser}`);
        return data.messages || [];
    } catch (err) {
        console.error('❌ Ошибка загрузки истории:', err);
        return [];
    }
}

function renderContactList() {
    contactList.innerHTML = "";
    allUsers.forEach(id => {
        if (id === myId) return;
        const li = document.createElement("li");
        li.dataset.user = id;
        li.classList.add("contact");
        li.classList.add(onlineUsers.has(id) ? "online" : "offline");
        li.textContent = id;
        
        li.onclick = async () => {
            // ✅ Правило 4: При смене диалога
            // 1. Очистить чат
            // 2. Загрузить историю с сервера (актуальная версия)
            // 3. Установить targetPeer
            
            const newDialogKey = getDialogKey(myId, id);
            console.log(`📁 Переключение на диалог: ${id} (key: ${newDialogKey})`);
            console.log(`🗑️ Очистка чата, сообщений: ${chatEl.children.length}`);
            
            // Сбрасываем targetPeer перед установкой нового
            targetPeer = null;
            
            // Очищаем чат
            chatEl.innerHTML = "";
            
            // Устанавливаем новый targetPeer
            targetPeer = id;
            
            // Загружаем историю с сервера (актуальная версия)
            const serverMessages = await loadHistory(id);
            
            if (serverMessages.length > 0) {
                console.log(`📤 Рендеринг ${serverMessages.length} сообщений из истории сервера`);
                serverMessages.forEach(msg => {
                    // Рендерим напрямую через renderMessageElement
                    renderMessageElement(msg.from, msg.payload, msg.mediaType);
                });
            } else {
                // Если серверная история пуста, показываем локальный буфер (как резерв)
                const bufferedMessages = messageBuffers[newDialogKey] || [];
                console.log(`📤 Загружено ${bufferedMessages.length} сообщений из локального буфера`);
                bufferedMessages.forEach(msg => {
                    renderMessageElement(msg.from, msg.payload, msg.type);
                });
                delete messageBuffers[newDialogKey];
            }
            
            document.querySelectorAll("#contactList li").forEach(el => el.classList.remove("active"));
            li.classList.add("active");
            if (window.ui?.openChat) window.ui.openChat(id);
        };
        contactList.appendChild(li);
    });
    // adjustChatContainer(); // CSS flexbox handles layout
}

// function adjustChatContainer() {
//     const chatContainer = document.querySelector('.chat-container');
//     if (!chatContainer) return;
//     chatContainer.style.height = '';
//     chatContainer.style.flex = '1';
//     const chatArea = document.querySelector('.chat-area');
//     if (chatArea) {
//         chatArea.style.display = 'flex';
//         chatArea.style.flexDirection = 'column';
//         chatArea.style.height = '100%';
//     }
//     // adjustChatMessagesPadding();
// }

/**
 * Отображает сообщение в чате согласно правилам диалога.
 * 
 * Правила:
 * 1. Всегда определяем dialogKey для сообщения
 * 2. Если targetPeer установлен И dialogKey совпадает с текущим диалогом → показать
 * 3. Иначе → буферизировать (включая targetPeer === null)
 * 
 * @param {string} from - Отправитель сообщения
 * @param {string} to - Получатель сообщения (может быть null)
 * @param {string|Blob} payload - Текст или base64-медиа
 * @param {'text'|'voice'|'video'} type - Тип сообщения
 * @returns {void}
 */
function displayMessage(from, to, payload, type = 'text') {
    // Всегда определяем dialogKey для сообщения
    // Если to отсутствует — используем myId как второй участник
    const dialogKey = to ? getDialogKey(from, to) : getDialogKey(myId, from);
    
    // Если targetPeer установлен и dialogKey совпадает — показываем сразу
    if (targetPeer) {
        const currentDialogKey = getDialogKey(myId, targetPeer);
        
        if (dialogKey === currentDialogKey) {
            console.log(`📨 [${dialogKey}] от ${from} к ${to}: показано (текущий диалог)`);
            return renderMessageElement(from, payload, type);
        }
    }
    
    // Во всех остальных случаях — буферизируем
    bufferMessage(dialogKey, from, to, payload, type);
    console.log(`📨 [${dialogKey}] от ${from} к ${to}: забуферизировано`);
}

// Вынесённая функция рендеринга элемента сообщения (для переиспользования)
function renderMessageElement(from, payload, type) {
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
    
    // adjustChatMessagesPadding(); // CSS flexbox handles layout
}

// function adjustChatMessagesPadding() {
//     const inputContainer = document.querySelector('.chat-input-container');
//     const messagesContainer = document.querySelector('.chat-messages');
//     if (!inputContainer || !messagesContainer) return;
//     const inputHeight = inputContainer.offsetHeight;
//     messagesContainer.style.paddingBottom = `${inputHeight}px`;
// }

// window.addEventListener('resize', adjustChatMessagesPadding);
// document.addEventListener('DOMContentLoaded', () => {
//     adjustChatMessagesPadding();
//     adjustChatContainer();
// });

// Вспомогательная функция для экранирования HTML (безопасный вывод)
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Обновляет статус всех забуферизированных сообщений (⏳ → ✓ или ❌).
 * Вызывается при восстановлении соединения или ошибке WebSocket.
 * 
 * @param {string} status - Символ статуса ('✓' или '❌')
 * @param {boolean} failed - Если true, сообщение можно кликнуть для повторной отправки
 */
function updateQueuedMessageStatus(status, failed = false) {
    const queuedElements = chatEl.querySelectorAll('.msg.mine.queued');
    queuedElements.forEach(queuedEl => {
        const content = queuedEl.dataset.queuedContent || '';
        queuedEl.classList.remove('queued');
        
        if (failed) {
            // При ошибке: ❌ + клик для повторной отправки
            queuedEl.innerHTML = `<span style="color: #f66;">❌ ${escapeHtml(content)}</span>`;
            queuedEl.style.cursor = 'pointer';
            queuedEl.onclick = () => {
                queuedEl.remove();
                msgInput.value = content;
                sendMessage();
            };
        } else {
            // При успехе: ✓
            queuedEl.innerHTML = `<span style="color: #4f4;">✓ ${escapeHtml(content)}</span>`;
        }
    });
}

const sendMessage = () => {
    const text = msgInput.value.trim();
    if (!text || !targetPeer) return;
    
    if (!ws || ws.readyState !== WebSocket.OPEN) {
        // Ограничиваем размер очереди — удаляем старое если превышен лимит
        if (messageQueue.length >= MAX_MESSAGE_QUEUE) {
            const removed = messageQueue.shift();
            console.warn(`⚠️ Очередь заполнена, удалено сообщение: ${removed.substring(0, 50)}...`);
            showToast('Старые сообщения удалены из очереди');
        }
        
        // Ставим в очередь для отправки при восстановлении соединения
        const packet = JSON.stringify({ type: "chat", to: targetPeer, payload: text, mediaType: 'text' });
        messageQueue.push(packet);
        
        // Визуальный индикатор: показываем сообщение с пометкой ⏳
        const queuedEl = document.createElement("div");
        queuedEl.className = 'msg mine queued';
        queuedEl.innerHTML = `<span style="color: #888;">⏳ ${escapeHtml(text)}</span>`;
        queuedEl.dataset.queuedContent = text;
        chatEl.appendChild(queuedEl);
        chatEl.scrollTop = chatEl.scrollHeight;
        
        msgInput.value = "";
        msgInput.dispatchEvent(new Event("input"));
        
        // Показываем тост
        showToast('Нет соединения — сообщение в очереди');
        console.warn('⚠️ WebSocket не готов, сообщение поставлено в очередь');
        return;
    }
    
    try {
        ws.send(JSON.stringify({ type: "chat", to: targetPeer, payload: text, mediaType: 'text' }));
        // Передаём: from=myId, to=targetPeer
        displayMessage(myId, targetPeer, text, 'text');
        msgInput.value = "";
        msgInput.dispatchEvent(new Event("input"));
    } catch (err) {
        console.error('❌ Ошибка отправки:', err);
    }
};

// Авто-ресайз textarea при вводе
function autoResizeTextarea() {
    msgInput.style.height = 'auto';
    msgInput.style.height = (msgInput.scrollHeight) + 'px';
}

// Add event listeners for sending messages
msgInput.addEventListener('input', () => {
    autoResizeTextarea();
});

msgInput.addEventListener('keydown', (e) => {
    // Ctrl+Enter или Cmd+Enter (Mac) = отправить
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        sendMessage();
    }
    // Enter или Shift+Enter = новая строка (дефолтное поведение textarea)
});

const sendBtn = document.getElementById('sendBtn');
if (sendBtn) {
    sendBtn.addEventListener('click', sendMessage);
}

// Инициализация textarea при загрузке
autoResizeTextarea();