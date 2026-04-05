const myId = prompt("Твой ID (латиница):") || "user_" + Math.random().toString(36).slice(2,6);
if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    alert("⚠️ Доступ к микрофону невозможен.\n\nПричины:\n• Сайт открыт по HTTP (не HTTPS)\n• Это не localhost\n\nРешение: открой через HTTPS (ngrok) или используй Firefox");
    throw new Error("mediaDevices not available");
}
let targetPeer = null;
let pc = null;
let localStream = null;
let pendingOffer = null;

const chatEl = document.getElementById("chat");
const contactList = document.getElementById("contactList");
const targetDisplay = document.getElementById("targetDisplay");
const callBtn = document.getElementById("callBtn");
const hangupBtn = document.getElementById("hangupBtn");
const msgInput = document.getElementById("msgInput");
const sendBtn = document.getElementById("sendBtn");
const myIdEl = document.getElementById("myId");
const callPanel = document.getElementById("incomingCallPanel");
const callerNameEl = document.getElementById("callerName");
const acceptBtn = document.getElementById("acceptCallBtn");
const rejectBtn = document.getElementById("rejectCallBtn");

myIdEl.textContent = `Ты: ${myId}`;
const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(`${protocol}//${location.host}/ws?user_id=${myId}`);

function log(text, cls = "msg") {
    const div = document.createElement("div");
    div.className = cls;
    div.textContent = text;
    chatEl.appendChild(div);
    chatEl.scrollTop = chatEl.scrollHeight;
}
log("✅ Подключено к серверу", "sys");

ws.onmessage = async (e) => {
    const msg = JSON.parse(e.data);
    switch (msg.type) {
        case "contacts": renderContacts(msg.payload); break;
        case "offer": handleOffer(msg.payload, msg.from); break;
        case "answer": handleAnswer(msg.payload); break;
        case "ice-candidate": handleICE(msg.payload); break;
        case "chat": log(`${msg.from}: ${msg.payload}`); break;
        case "hangup":
            if (pc || localStream) {
                log("📵 Собеседник завершил звонок", "sys");
                cleanup();
            }
            break;
        case "reject":
            if (!pc) {
                log("❌ Звонок отклонён", "sys");
                cleanup();
            }
            break;
    }
};

function renderContacts(users) {
    contactList.innerHTML = "";
    users.forEach(id => {
        if (id === myId) return;
        const li = document.createElement("li");
        li.textContent = id;
        li.onclick = () => selectPeer(id);
        if (id === targetPeer) li.classList.add("active");
        contactList.appendChild(li);
    });
}

function selectPeer(id) {
    targetPeer = id;
    targetDisplay.value = id;
    // Кнопки активны, если нет активного звонка
    if (!pc) {
        callBtn.disabled = false;
        sendBtn.disabled = false;
    }
    document.querySelectorAll("#contactList li").forEach(li => {
        li.classList.toggle("active", li.textContent === id);
    });
    log(`👤 Выбран контакт: ${id}`, "sys");
}

// === ИСХОДЯЩИЙ ЗВОНОК ===
callBtn.onclick = async () => {
    if (!targetPeer || pc) return;
    callBtn.disabled = true;
    hangupBtn.disabled = false;
    log(`📞 Вызываю ${targetPeer}...`, "sys");

    try {
        localStream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
    } catch (err) {
        log("🚫 Нет доступа к микрофону: " + err.message, "sys");
        cleanup(); return;
    }

    setupPC(targetPeer);
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    ws.send(JSON.stringify({ type: "offer", to: targetPeer, payload: offer }));
};

// === ВХОДЯЩИЙ ЗВОНОК ===
acceptBtn.onclick = async () => {
    if (!pendingOffer || pc) return;
    const { from, offer } = pendingOffer;
    callPanel.style.display = "none";
    pendingOffer = null;
    targetPeer = from;
    selectPeer(from);

    callBtn.disabled = true;
    hangupBtn.disabled = false;

    try {
        localStream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
    } catch (err) {
        log("🚫 Нет доступа к микрофону: " + err.message, "sys");
        ws.send(JSON.stringify({ type: "reject", to: from, payload: null }));
        cleanup(); return;
    }

    setupPC(from);
    await pc.setRemoteDescription(new RTCSessionDescription(offer));
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    ws.send(JSON.stringify({ type: "answer", to: from, payload: answer }));
    log("📞 Звонок принят", "sys");
};

rejectBtn.onclick = () => {
    if (!pendingOffer) return;
    const { from } = pendingOffer;
    callPanel.style.display = "none";
    ws.send(JSON.stringify({ type: "reject", to: from, payload: null }));
    pendingOffer = null;
    log("❌ Звонок отклонён", "sys");
};

hangupBtn.onclick = () => {
    if (!targetPeer) return;
    ws.send(JSON.stringify({ type: "hangup", to: targetPeer, payload: null }));
    cleanup();
};

sendBtn.onclick = () => {
    const text = msgInput.value.trim();
    if (!text || !targetPeer) return;
    ws.send(JSON.stringify({ type: "chat", to: targetPeer, payload: text }));
    log(`Ты: ${text}`);
    msgInput.value = "";
};

function setupPC(target) {
    pc = new RTCPeerConnection({ iceServers: [{ urls: "stun:stun.l.google.com:19302" }] });
    localStream.getTracks().forEach(track => pc.addTrack(track, localStream));

    pc.ontrack = (e) => {
        log("🔊 Аудиопоток получен", "sys");
        document.getElementById("audioRemote").srcObject = e.streams[0];
    };
    pc.onicecandidate = (e) => {
        if (e.candidate) ws.send(JSON.stringify({ type: "ice-candidate", to: target, payload: e.candidate }));
    };
    pc.onconnectionstatechange = () => {
        log(`🌐 Статус WebRTC: ${pc.connectionState}`, "sys");
        if (pc.connectionState === "disconnected" || pc.connectionState === "failed") cleanup();
    };
}

async function handleOffer(offer, from) {
    if (pc) {
        log("⚠️ Уже в разговоре. Отклоняю автоматически.", "sys");
        ws.send(JSON.stringify({ type: "reject", to: from, payload: null }));
        return;
    }
    pendingOffer = { offer, from };
    callerNameEl.textContent = from;
    callPanel.style.display = "block";
    log(`📥 Входящий звонок от ${from}`, "sys");
    selectPeer(from);
}

async function handleAnswer(answer) {
    if (!pc) return;
    log("📡 Ответ принят", "sys");
    await pc.setRemoteDescription(new RTCSessionDescription(answer));
}

async function handleICE(candidate) {
    if (!pc) return;
    await pc.addIceCandidate(new RTCIceCandidate(candidate)).catch(() => {});
}

// 🔑 ИСПРАВЛЕННАЯ ОЧИСТКА
function cleanup() {
    if (!localStream && !pc) return; // Уже очищено

    if (localStream) {
        localStream.getTracks().forEach(t => t.stop());
        localStream = null;
    }
    if (pc) {
        pc.close();
        pc = null;
    }
    pendingOffer = null;
    callPanel.style.display = "none";

    // ✅ Логика кнопок: disabled = true, когда УСЛОВИЕ ЛОЖНО
    callBtn.disabled = !targetPeer;   // Включить, если контакт выбран
    hangupBtn.disabled = true;        // Всегда выключать после завершения
    sendBtn.disabled = !targetPeer;   // Включить чат, если контакт выбран
    
    log("🔇 Звонок завершён, готов к новому", "sys");
}