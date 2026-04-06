const myId = prompt("Твой ID:") || "user_" + Math.random().toString(36).slice(2,6);
let targetPeer = null;

document.getElementById("myId").textContent = myId;

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

const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(`${protocol}//${location.host}/ws?user_id=${myId}`);

ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "contacts") renderContacts(msg.payload);
    if (msg.type === "chat") displayMessage(msg.from, msg.payload, msg.mediaType);
};

function displayMessage(from, payload, type = 'text') {
    const div = document.createElement("div");
    div.className = `msg ${from === myId ? 'mine' : ''}`;
    
    const name = from === myId ? "Вы" : from;

    if (type === 'text') {
        div.innerHTML = `<strong>${name}</strong>${payload}`;
    } else if (type === 'voice') {
        const audio = document.createElement("audio");
        audio.src = payload;
        audio.controls = true;
        div.innerHTML = `<strong>${name}</strong>`;
        div.appendChild(audio);
    } else if (type === 'video') {
        const video = document.createElement("video");
        video.src = payload;
        video.className = "video-circle";
        video.loop = true;
        video.onclick = () => video.paused ? video.play() : video.pause();
        div.innerHTML = `<strong>${name}</strong>`;
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

msgInput.onkeydown = (e) => { if (e.key === "Enter") sendMessage(); };
document.getElementById("sendBtn").onclick = sendMessage;