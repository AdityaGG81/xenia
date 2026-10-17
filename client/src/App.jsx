import { useCallback, useEffect, useRef, useState } from 'react';
import { NodeAPI } from './api';
import './App.css';

function newCallID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  if (globalThis.crypto?.getRandomValues) {
    const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
    return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
  }
  throw new Error('This browser cannot create a secure call ID. Open Xenia over HTTPS.');
}

function App() {
  const [url, setUrl] = useState(window.location.origin);
  const [username, setUsername] = useState('');
  const [connection, setConnection] = useState(null);
  const [error, setError] = useState('');
  const [connecting, setConnecting] = useState(false);

  const connect = useCallback(async (targetUrl, name, savedDeviceID) => {
    const api = new NodeAPI(targetUrl);
    const health = await api.health();
    const identity = await api.register(name, savedDeviceID || '');
    let identityWarning = '';
    try { localStorage.setItem('xenia.identity', JSON.stringify({ url: api.baseUrl, username: name, device_id: identity.device_id })); }
    catch { identityWarning = 'This browser cannot save your device identity. Reopening the app may create a new session.'; }
    setConnection({ api, username: name, deviceId: identity.device_id, nodeId: health.node_id, storageState: health.storage, identityWarning });
    setError('');
  }, []);

  useEffect(() => {
    try {
      const saved = JSON.parse(localStorage.getItem('xenia.identity') || 'null');
      if (saved?.url && saved?.username && saved?.device_id) {
        setUrl(saved.url);
        setUsername(saved.username);
        connect(saved.url, saved.username, saved.device_id).catch(cause => setError(cause.message || 'Could not reconnect to the saved node.'));
      }
    } catch { /* Ignore invalid local identity data. */ }
  }, [connect]);

  const handleConnect = async (event) => {
    event.preventDefault();
    const name = username.trim();
    let targetUrl = url.trim();
    if (!targetUrl.startsWith('http://') && !targetUrl.startsWith('https://')) targetUrl = `http://${targetUrl}`;
    if (!name || !targetUrl) return;
    if (![...name].length || [...name].length > 32 || !/^[\p{L}\p{N}._-]+$/u.test(name)) {
      setError('Use 1–32 letters, numbers, dots, dashes, or underscores for your name.');
      return;
    }
    setConnecting(true);
    try {
      const api = new NodeAPI(targetUrl);
      const saved = JSON.parse(localStorage.getItem('xenia.identity') || 'null');
      await connect(api.baseUrl, name, saved?.url === api.baseUrl && saved?.username === name ? saved.device_id : null);
    } catch (cause) {
      setError(cause.message || 'Could not connect to this Xenia node.');
    } finally { setConnecting(false); }
  };

  const disconnect = () => {
    localStorage.removeItem('xenia.identity');
    setConnection(null);
  };

  if (connection) return <ChatInterface {...connection} onDisconnect={disconnect} />;

  return (
    <div className="app-container connection-screen animate-fade-in">
      <div className="glass-panel connection-card">
        <h1 className="gradient-text">Xenia Mesh</h1>
        <p className="subtitle">Connect to a nearby node</p>
        <form onSubmit={handleConnect}>
          <div className="input-group"><label>Node address</label><input className="user-input" value={url} onChange={event => setUrl(event.target.value)} placeholder="http://192.168.4.1:8080" /></div>
          <div className="input-group"><label>Your name</label><input className="user-input" value={username} onChange={event => setUsername(event.target.value)} placeholder="e.g. Alice" autoComplete="nickname" /></div>
          {error && <div className="error-message">{error}</div>}
          <button className="connect-btn" disabled={connecting}>{connecting ? 'Connecting…' : 'Connect to mesh'}</button>
        </form>
      </div>
    </div>
  );
}

function ChatInterface({ api, username, deviceId, nodeId, storageState, identityWarning, onDisconnect }) {
  const [users, setUsers] = useState([]);
  const [messages, setMessages] = useState([]);
  const [selectedUser, setSelectedUser] = useState(null);
  const [draft, setDraft] = useState('');
  const [socketState, setSocketState] = useState('connecting');
  const [incomingCall, setIncomingCall] = useState(null);
  const [callStatus, setCallStatus] = useState('');
  const [remoteStream, setRemoteStream] = useState(null);
  const [actionError, setActionError] = useState('');
  const socketRef = useRef(null);
  const peerRef = useRef(null);
  const localStreamRef = useRef(null);
  const activePeerName = useRef('');
  const activeCallID = useRef('');
  const pendingCandidates = useRef(new Map());
  const localCandidateQueue = useRef([]);
  const localCandidatesReady = useRef(false);
  const incomingCallRef = useRef(null);
  const callTimerRef = useRef(null);
  const recoveryTimerRef = useRef(null);
  const callBusyRef = useRef(false);
  const answeringCallRef = useRef(false);
  const callGenerationRef = useRef(0);
  const audioRef = useRef(null);
  const bottomRef = useRef(null);

  const send = useCallback((payload) => {
    if (socketRef.current?.readyState === WebSocket.OPEN) {
      try {
        socketRef.current.send(JSON.stringify(payload));
        return true;
      } catch {
        return false;
      }
    }
    return false;
  }, []);

  const addMessage = useCallback((message) => {
    if (!message?.id) return;
    setMessages(current => {
      const index = current.findIndex(item => item.id === message.id);
      if (index < 0) return [...current, message];
      const next = [...current];
      next[index] = { ...next[index], ...message };
      return next;
    });
  }, []);

  const closeCall = useCallback(() => {
    callGenerationRef.current += 1;
    callBusyRef.current = false;
    answeringCallRef.current = false;
    clearTimeout(callTimerRef.current);
    callTimerRef.current = null;
    clearTimeout(recoveryTimerRef.current);
    recoveryTimerRef.current = null;
    peerRef.current?.close();
    peerRef.current = null;
    localStreamRef.current?.getTracks().forEach(track => track.stop());
    localStreamRef.current = null;
    setRemoteStream(null);
    setIncomingCall(null);
    incomingCallRef.current = null;
    setCallStatus('');
    activePeerName.current = '';
    activeCallID.current = '';
    pendingCandidates.current.clear();
    localCandidateQueue.current = [];
    localCandidatesReady.current = false;
    if (audioRef.current) audioRef.current.srcObject = null;
  }, []);

  const failCall = useCallback((message, notifyPeer = true) => {
    const peer = activePeerName.current || incomingCallRef.current?.from;
    const callID = activeCallID.current || incomingCallRef.current?.call_id;
    if (notifyPeer && peer && callID) send({ type: 'call_hangup', to: peer, call_id: callID });
    closeCall();
    setCallStatus(message);
  }, [closeCall, send]);

  const applyPendingCandidates = useCallback(async (pc, callID) => {
    const queued = pendingCandidates.current.get(callID)?.candidates || [];
    pendingCandidates.current.delete(callID);
    for (const candidate of queued) {
      try { await pc.addIceCandidate(JSON.parse(candidate)); }
      catch (cause) { console.warn('Could not apply queued ICE candidate', cause); }
    }
  }, []);

  const flushLocalCandidates = useCallback((peerName, callID) => {
    localCandidatesReady.current = true;
    const queued = localCandidateQueue.current.splice(0);
    for (const candidate of queued) {
      if (!send({ type: 'ice_candidate', to: peerName, call_id: callID, candidate })) return false;
    }
    return true;
  }, [send]);

  const makePeer = useCallback((peerName) => {
    peerRef.current?.close();
    const pc = new RTCPeerConnection({ iceServers: [], iceCandidatePoolSize: 4 });
    peerRef.current = pc;
    activePeerName.current = peerName;
    pc.onicecandidate = event => {
      if (event.candidate) {
        const candidate = JSON.stringify(event.candidate);
        if (!localCandidatesReady.current) {
          if (localCandidateQueue.current.length < 64) localCandidateQueue.current.push(candidate);
        } else if (!send({ type: 'ice_candidate', to: peerName, call_id: activeCallID.current, candidate })) {
          failCall('Call ended because signaling disconnected.');
        }
      }
    };
    pc.ontrack = event => {
      const stream = event.streams?.[0] || new MediaStream([event.track]);
      setRemoteStream(stream);
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'connected') setCallStatus('Connected');
      if (pc.connectionState === 'connected') {
        clearTimeout(callTimerRef.current);
        callTimerRef.current = null;
        clearTimeout(recoveryTimerRef.current);
        recoveryTimerRef.current = null;
      } else if (pc.connectionState === 'failed' && peerRef.current === pc) {
        failCall('Call could not connect. Check the mesh link and try again.');
      } else if (pc.connectionState === 'disconnected' && peerRef.current === pc) {
        setCallStatus('Connection interrupted…');
        if (!recoveryTimerRef.current) {
          recoveryTimerRef.current = setTimeout(() => {
            recoveryTimerRef.current = null;
            if (peerRef.current === pc && pc.connectionState === 'disconnected') {
              failCall('Call ended because the connection was lost.');
            }
          }, 15000);
        }
      }
    };
    return pc;
  }, [failCall, send]);

  const handleEvent = useCallback(async (event) => {
    if (event.type === 'presence_sync') setUsers(event.users || []);
    if (event.type === 'user_online' && event.user) setUsers(current => [...current.filter(user => user.device_id !== event.user.device_id), event.user]);
    if (event.type === 'user_offline') setUsers(current => current.filter(user => user.username !== event.username));
    if (event.type === 'message_history') (event.messages || []).forEach(message => {
      addMessage(message);
      if (message.to === username) send({ type: 'message_ack', message_id: message.id });
    });
    if (event.type === 'new_message') {
      addMessage(event.message);
      if (event.message?.to === username) send({ type: 'message_ack', message_id: event.message.id });
    }
    if (event.type === 'message_sent' || event.type === 'message_status') addMessage(event.message);
    if (event.type === 'call_offer') {
      if (peerRef.current || incomingCallRef.current || callBusyRef.current) send({ type: 'call_reject', to: event.from, call_id: event.call_id });
      else {
        callBusyRef.current = true;
        incomingCallRef.current = event;
        setIncomingCall(event);
        setCallStatus(`Incoming call from ${event.from}`);
        callTimerRef.current = setTimeout(() => {
          if (incomingCallRef.current?.call_id === event.call_id) {
            send({ type: 'call_reject', to: event.from, call_id: event.call_id });
            closeCall();
            setCallStatus('Missed call');
          }
        }, 30000);
      }
    }
    if (event.type === 'call_answer' && peerRef.current && event.sdp && event.call_id === activeCallID.current && event.from === activePeerName.current) {
      const pc = peerRef.current;
      try {
        await pc.setRemoteDescription({ type: 'answer', sdp: event.sdp });
        if (peerRef.current !== pc) return;
        clearTimeout(callTimerRef.current);
        callTimerRef.current = setTimeout(() => {
          if (peerRef.current === pc && pc.connectionState !== 'connected') failCall('Call could not connect. Check the mesh link and try again.');
        }, 30000);
        await applyPendingCandidates(pc, event.call_id);
        setCallStatus('Connecting…');
      } catch (cause) {
        failCall(cause.message || 'Could not apply the call answer.');
      }
    }
    if (event.type === 'ice_candidate' && event.candidate) {
      const callID = event.call_id || '';
      const matchesActive = callID && callID === activeCallID.current && event.from === activePeerName.current;
      const matchesIncoming = callID && callID === incomingCallRef.current?.call_id && event.from === incomingCallRef.current?.from;
      if (!callID || !event.from) return;
      const now = Date.now();
      for (const [queuedCallID, queue] of pendingCandidates.current) {
        if (queue.expiresAt <= now) pendingCandidates.current.delete(queuedCallID);
      }
      const queueCandidate = () => {
        if (pendingCandidates.current.size >= 8 && !pendingCandidates.current.has(callID)) {
          const oldest = [...pendingCandidates.current.entries()].sort((a, b) => a[1].expiresAt - b[1].expiresAt)[0];
          if (oldest) pendingCandidates.current.delete(oldest[0]);
        }
        let queued = pendingCandidates.current.get(callID);
        if (!queued) queued = { candidates: [], expiresAt: now + 60000 };
        if (queued.candidates.length < 64) queued.candidates.push(event.candidate);
        queued.expiresAt = now + 60000;
        pendingCandidates.current.set(callID, queued);
      };
      if (!matchesActive && !matchesIncoming) {
        queueCandidate();
        return;
      }
      if (!peerRef.current || !peerRef.current.remoteDescription) queueCandidate();
      else {
        try { await peerRef.current.addIceCandidate(JSON.parse(event.candidate)); }
        catch (cause) { console.warn('Could not apply ICE candidate', cause); }
      }
    }
    if ((event.type === 'call_hangup' || event.type === 'call_reject') && event.call_id === activeCallID.current && event.from === activePeerName.current) {
      closeCall();
      setCallStatus(event.type === 'call_reject' ? 'Call declined' : 'Call ended');
    }
    if ((event.type === 'call_hangup' || event.type === 'call_reject') && event.call_id === incomingCallRef.current?.call_id && event.from === incomingCallRef.current?.from) {
      closeCall();
      setCallStatus(event.type === 'call_reject' ? 'Call declined' : 'Call ended');
    }
    if (event.type === 'error') setActionError(event.error || 'The node rejected an action.');
  }, [addMessage, applyPendingCandidates, closeCall, failCall, send, username]);

  useEffect(() => {
    let reconnectTimer;
    let heartbeatTimer;
    let disposed = false;
    let attempts = 0;
    const open = () => {
      if (disposed) return;
      let socket;
      try {
        socket = api.openSocket(deviceId, {
        onOpen: () => {
          attempts = 0;
          setSocketState('online');
          socket.send(JSON.stringify({ type: 'heartbeat' }));
          heartbeatTimer = setInterval(() => {
            if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'heartbeat' }));
          }, 5000);
        },
        onMessage: message => handleEvent(message).catch(cause => console.warn('Could not handle node event', cause)),
        onClose: () => {
          clearInterval(heartbeatTimer);
          if (!disposed) {
            const callWasActive = Boolean(peerRef.current || incomingCallRef.current);
            if (callWasActive) {
              closeCall();
              setCallStatus('Call ended because signaling disconnected.');
            }
            setSocketState('reconnecting');
            const delay = Math.min(1000 * (2 ** attempts), 30000) + Math.random() * 500;
            attempts += 1;
            reconnectTimer = setTimeout(open, delay);
          }
        },
        onError: () => setSocketState('reconnecting'),
        });
      } catch (cause) {
        console.warn('Could not create node WebSocket', cause);
        setSocketState('reconnecting');
        reconnectTimer = setTimeout(open, 3000);
        return;
      }
      socketRef.current = socket;
    };
    open();
    return () => {
      disposed = true;
      clearTimeout(reconnectTimer);
      clearInterval(heartbeatTimer);
      clearTimeout(callTimerRef.current);
      clearTimeout(recoveryTimerRef.current);
      socketRef.current?.close();
      socketRef.current = null;
      peerRef.current?.close();
      localStreamRef.current?.getTracks().forEach(track => track.stop());
      localStreamRef.current = null;
      pendingCandidates.current.clear();
    };
  }, [api, closeCall, deviceId, handleEvent]);

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [messages, selectedUser]);
  useEffect(() => { if (audioRef.current && remoteStream) audioRef.current.srcObject = remoteStream; }, [remoteStream]);

  const sendMessage = (event) => {
    event.preventDefault();
    if (!draft.trim() || !selectedUser) return;
    if (!send({ type: 'send_message', to: selectedUser, body: draft.trim() })) {
      setActionError('The node is reconnecting. Your message was not sent; try again when it is online.');
      return;
    }
    setActionError('');
    setDraft('');
  };

  const startCall = async () => {
    if (!selectedUser || callBusyRef.current || peerRef.current || incomingCallRef.current) return;
    callBusyRef.current = true;
    const generation = ++callGenerationRef.current;
    setCallStatus('Requesting microphone…');
    try {
      if (!navigator.mediaDevices?.getUserMedia) throw new Error('Audio calls require HTTPS with a trusted certificate on this phone.');
      if (socketRef.current?.readyState !== WebSocket.OPEN) throw new Error('The node is reconnecting. Try the call again when it is online.');
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (generation !== callGenerationRef.current) {
        stream.getTracks().forEach(track => track.stop());
        return;
      }
      localStreamRef.current = stream;
      activeCallID.current = newCallID();
      const pc = makePeer(selectedUser);
      stream.getTracks().forEach(track => pc.addTrack(track, stream));
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      if (generation !== callGenerationRef.current || peerRef.current !== pc) return;
      if (!send({ type: 'call_offer', to: selectedUser, call_id: activeCallID.current, sdp: offer.sdp })) throw new Error('The connection dropped before the call offer was sent.');
      if (!flushLocalCandidates(selectedUser, activeCallID.current)) throw new Error('The connection dropped while sending call setup.');
      setCallStatus(`Calling ${selectedUser}…`);
      const callID = activeCallID.current;
      callTimerRef.current = setTimeout(() => {
        if (activeCallID.current === callID && pc.connectionState !== 'connected') failCall('No answer.');
      }, 45000);
    } catch (cause) {
      if (generation === callGenerationRef.current) failCall(cause.message || 'Could not start audio call', Boolean(activeCallID.current));
    }
  };

  const answerCall = async () => {
    const pendingCall = incomingCallRef.current;
    if (!pendingCall || !callBusyRef.current || answeringCallRef.current || peerRef.current) return;
    answeringCallRef.current = true;
    const generation = ++callGenerationRef.current;
    try {
      if (!navigator.mediaDevices?.getUserMedia) throw new Error('Audio calls require HTTPS with a trusted certificate on this phone.');
      if (socketRef.current?.readyState !== WebSocket.OPEN) throw new Error('The node is reconnecting. Try again when it is online.');
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (generation !== callGenerationRef.current || incomingCallRef.current?.call_id !== pendingCall.call_id) {
        stream.getTracks().forEach(track => track.stop());
        return;
      }
      localStreamRef.current = stream;
      activeCallID.current = pendingCall.call_id;
      const pc = makePeer(pendingCall.from);
      stream.getTracks().forEach(track => pc.addTrack(track, stream));
      await pc.setRemoteDescription({ type: 'offer', sdp: pendingCall.sdp });
      if (generation !== callGenerationRef.current || peerRef.current !== pc) return;
      clearTimeout(callTimerRef.current);
      callTimerRef.current = null;
      await applyPendingCandidates(pc, activeCallID.current);
      if (generation !== callGenerationRef.current || peerRef.current !== pc) return;
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      if (generation !== callGenerationRef.current || peerRef.current !== pc) return;
      if (!send({ type: 'call_answer', to: pendingCall.from, call_id: activeCallID.current, sdp: answer.sdp })) throw new Error('The node disconnected before the answer was sent.');
      if (!flushLocalCandidates(pendingCall.from, activeCallID.current)) throw new Error('The connection dropped while sending call setup.');
      setIncomingCall(null);
      incomingCallRef.current = null;
      setCallStatus('Connecting…');
      const callID = activeCallID.current;
      callTimerRef.current = setTimeout(() => {
        if (activeCallID.current === callID && pc.connectionState !== 'connected') failCall('Call could not connect. Check the mesh link and try again.');
      }, 30000);
    } catch (cause) {
      if (generation === callGenerationRef.current) failCall(cause.message || 'Could not answer call');
    }
  };

  const endCall = () => {
    if (activePeerName.current && activeCallID.current) send({ type: 'call_hangup', to: activePeerName.current, call_id: activeCallID.current });
    else if (incomingCallRef.current) send({ type: 'call_reject', to: incomingCallRef.current.from, call_id: incomingCallRef.current.call_id });
    closeCall();
  };
  const callIsActive = Boolean(peerRef.current || incomingCallRef.current);
  const conversation = messages.filter(message => (message.from === username && message.to === selectedUser) || (message.from === selectedUser && message.to === username));
  const contacts = users.filter(user => user.username !== username && user.online !== false);

  return (
    <div className="chat-interface animate-fade-in">
      <header className="glass-panel app-header"><div className="header-info"><h2>@{username}</h2><span className="node-badge">{nodeId} · {socketState} · storage {storageState || 'unknown'}</span></div><button onClick={onDisconnect} className="btn-secondary">Disconnect</button></header>
      {(identityWarning || storageState === 'degraded') && <div className="error-message" role="status">{identityWarning || 'This node cannot write its message store. Messages may not survive a restart.'}</div>}
      <div className="app-layout">
        <aside className="glass-panel sidebar"><h3>Nearby</h3>{contacts.length === 0 ? <p className="empty-text">No other users online</p> : <ul className="user-list">{contacts.map(user => <li key={user.device_id || user.username} className={selectedUser === user.username ? 'active' : ''} onClick={() => setSelectedUser(user.username)}><div className="user-avatar">{user.username[0]?.toUpperCase()}</div><span className="user-name">@{user.username}</span><div className="status-indicator online" /></li>)}</ul>}</aside>
        <main className="glass-panel chat-area">{selectedUser ? <><div className="chat-header"><h3>Chat with @{selectedUser}</h3><button onClick={startCall} className="btn-call" disabled={socketState !== 'online' || Boolean(callStatus)}>📞 Call</button></div>{actionError && <div className="error-message" role="status">{actionError}</div>}<div className="message-list">{conversation.map(message => <div key={message.id} className={`message-bubble ${message.from === username ? 'sent' : 'received'}`}><div className="message-content">{message.body}</div><div className="message-meta">{new Date(message.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} · {message.state}</div></div>)}<div ref={bottomRef} /></div><form className="message-composer" onSubmit={sendMessage}><input value={draft} onChange={event => setDraft(event.target.value)} placeholder={socketState === 'online' ? 'Type a message…' : 'Reconnecting…'} disabled={socketState !== 'online'} /><button disabled={!draft.trim() || socketState !== 'online'}>Send</button></form></> : <div className="select-prompt"><div className="icon">💬</div><p>Select someone to start chatting</p>{actionError && <div className="error-message" role="status">{actionError}</div>}</div>}</main>
      </div>
      {(incomingCall || callStatus) && <div className="call-overlay glass-panel"><h3>{incomingCall ? 'Incoming audio call' : callStatus}</h3>{incomingCall && <><p>from @{incomingCall.from}</p><div className="call-actions"><button className="btn-answer" onClick={answerCall} disabled={socketState !== 'online' || answeringCallRef.current}>Answer</button><button className="btn-reject" onClick={endCall}>Decline</button></div></>}<button className="btn-reject" onClick={endCall}>{callIsActive ? 'End call' : 'Dismiss'}</button></div>}
      <audio ref={audioRef} autoPlay playsInline controls={Boolean(remoteStream)} />
    </div>
  );
}

export default App;
