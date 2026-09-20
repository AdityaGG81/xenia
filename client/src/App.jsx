import { useState, useEffect, useRef } from 'react';
import { NodeAPI } from './api';
import './App.css'; // We'll replace this with our own layout styles if needed

function ConnectionScreen({ onConnect }) {
  const [url, setUrl] = useState('http://192.168.1.5:8081');
  const [username, setUsername] = useState('alice');
  const [error, setError] = useState('');
  const [connecting, setConnecting] = useState(false);

  const handleConnect = async (e) => {
    e.preventDefault();
    if (!url || !username) return;
    setConnecting(true);
    setError('');
    
    let finalUrl = url.trim();
    if (!finalUrl.startsWith('http://') && !finalUrl.startsWith('https://')) {
      finalUrl = 'http://' + finalUrl;
    }

    try {
      const api = new NodeAPI(finalUrl);
      const health = await api.health();
      onConnect(api, username, health.node_id);
    } catch (err) {
      setError('Failed to connect to node. Is the proxy running?');
    } finally {
      setConnecting(false);
    }
  };

  return (
    <div className="connection-screen animate-fade-in">
      <div className="glass-panel connection-card">
        <h1 className="gradient-text">Xenia Mesh Client</h1>
        <p className="subtitle">Connect to your local mesh node</p>
        
        <form onSubmit={handleConnect}>
          <div className="input-group">
            <label>Node URL</label>
            <input 
              type="text" 
              value={url} 
              onChange={e => setUrl(e.target.value)}
              placeholder="http://192.168.1.5:8081"
            />
          </div>
          
          <div className="input-group">
            <label>Username</label>
            <input 
              type="text" 
              value={username} 
              onChange={e => setUsername(e.target.value)}
              placeholder="e.g. alice"
            />
          </div>
          
          {error && <div className="error-message">{error}</div>}
          
          <button type="submit" disabled={connecting} className="connect-btn">
            {connecting ? 'Connecting...' : 'Connect to Mesh'}
          </button>
        </form>
      </div>
    </div>
  );
}

function ChatInterface({ api, username, nodeId, onDisconnect }) {
  const [presence, setPresence] = useState({});
  const [messages, setMessages] = useState([]);
  const [newMessage, setNewMessage] = useState('');
  const [selectedUser, setSelectedUser] = useState(null);
  
  // Call state
  const [incomingCall, setIncomingCall] = useState(null);
  const [activeCall, setActiveCall] = useState(null);
  
  const messagesEndRef = useRef(null);

  const fetchState = async () => {
    try {
      // 0. Broadcast our own presence
      await api.setPresence(username, 'online');

      // 1. Fetch network presence
      const p = await api.presence();
      setPresence(p);
      
      // 2. Fetch messages for the current user
      const msgs = await api.getMessages(username);
      setMessages(msgs || []);
      
      // 3. Scan calls for incoming calls to this user
      const callsRes = await api.getCalls();
      if (callsRes.calls) {
        for (const [callId, call] of Object.entries(callsRes.calls)) {
          if (call.to === username && call.state === 'ringing' && !activeCall) {
            setIncomingCall(call);
          } else if (activeCall && call.id === activeCall.id) {
            // Update active call state
            setActiveCall(call);
          }
        }
      }
    } catch (err) {
      console.error("Failed to sync state", err);
    }
  };

  useEffect(() => {
    fetchState();
    const interval = setInterval(fetchState, 2000);
    return () => clearInterval(interval);
  }, [api, username, activeCall]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, selectedUser]);

  const handleSend = async (e) => {
    e.preventDefault();
    if (!newMessage.trim() || !selectedUser) return;
    
    try {
      await api.sendMessage(username, selectedUser, newMessage);
      setNewMessage('');
      fetchState();
    } catch (err) {
      alert("Failed to send message");
    }
  };

  const handleCall = async (targetUser) => {
    try {
      const call = await api.createCall(username, targetUser);
      setActiveCall(call);
    } catch (err) {
      alert("Failed to initiate call");
    }
  };

  const answerCall = async () => {
    if (!incomingCall) return;
    try {
      await api.updateCallState(incomingCall.id, 'accepted');
      setActiveCall({ ...incomingCall, state: 'accepted' });
      setIncomingCall(null);
    } catch (err) {
      alert("Failed to answer");
    }
  };

  const rejectCall = async () => {
    if (!incomingCall) return;
    try {
      // Assuming 'rejected' or just hanging up is valid
      await api.updateCallState(incomingCall.id, 'rejected');
      setIncomingCall(null);
    } catch (err) {
      setIncomingCall(null);
    }
  };

  // Filter messages for the selected conversation
  const conversation = messages.filter(m => 
    (m.from === username && m.to === selectedUser) ||
    (m.from === selectedUser && m.to === username)
  );

  // Extract unique users from presence (excluding self)
  const users = presence.users 
    ? presence.users.map(u => u.user_id).filter(id => id !== username)
    : [];

  return (
    <div className="chat-interface animate-fade-in">
      {/* Header */}
      <header className="glass-panel app-header">
        <div className="header-info">
          <h2>@{username}</h2>
          <span className="node-badge">Node: {nodeId}</span>
        </div>
        <button onClick={onDisconnect} className="btn-secondary">Disconnect</button>
      </header>

      {/* Main Layout */}
      <div className="app-layout">
        {/* Sidebar: Users */}
        <aside className="glass-panel sidebar">
          <h3>Network</h3>
          {users.length === 0 ? (
            <p className="empty-text">No other users online</p>
          ) : (
            <ul className="user-list">
              {users.map(u => (
                <li 
                  key={u} 
                  className={selectedUser === u ? 'active' : ''}
                  onClick={() => setSelectedUser(u)}
                >
                  <div className="user-avatar">{u.charAt(0).toUpperCase()}</div>
                  <span className="user-name">@{u}</span>
                  <div className="status-indicator online"></div>
                </li>
              ))}
            </ul>
          )}
        </aside>

        {/* Chat Area */}
        <main className="glass-panel chat-area">
          {selectedUser ? (
            <>
              <div className="chat-header">
                <h3>Chat with @{selectedUser}</h3>
                <button onClick={() => handleCall(selectedUser)} className="btn-call">
                  📞 Call
                </button>
              </div>
              
              <div className="message-list">
                {conversation.length === 0 ? (
                  <div className="empty-chat">Say hello to @{selectedUser}!</div>
                ) : (
                  conversation.map(msg => (
                    <div key={msg.id} className={`message-bubble ${msg.from === username ? 'sent' : 'received'}`}>
                      <div className="message-content">{msg.body}</div>
                      <div className="message-meta">
                        {new Date(msg.timestamp).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'})} 
                        {msg.from === username && <span className="status-icon"> • {msg.state}</span>}
                      </div>
                    </div>
                  ))
                )}
                <div ref={messagesEndRef} />
              </div>

              <form className="message-composer" onSubmit={handleSend}>
                <input 
                  type="text" 
                  value={newMessage} 
                  onChange={e => setNewMessage(e.target.value)}
                  placeholder="Type a message..."
                />
                <button type="submit" disabled={!newMessage.trim()}>Send</button>
              </form>
            </>
          ) : (
            <div className="select-prompt">
              <div className="icon">💬</div>
              <p>Select a user to start chatting</p>
            </div>
          )}
        </main>
      </div>

      {/* Call Overlays */}
      {incomingCall && (
        <div className="call-overlay glass-panel animate-fade-in">
          <div className="call-avatar animate-pulse">📞</div>
          <h3>Incoming Call</h3>
          <p>from @{incomingCall.from}</p>
          <div className="call-actions">
            <button className="btn-answer" onClick={answerCall}>Answer</button>
            <button className="btn-reject" onClick={rejectCall}>Decline</button>
          </div>
        </div>
      )}

      {activeCall && (
        <div className="active-call-banner glass-panel">
          <div className="pulse-dot"></div>
          <span>Call with @{activeCall.to === username ? activeCall.from : activeCall.to} ({activeCall.state})</span>
          <button onClick={() => setActiveCall(null)} className="btn-reject">End</button>
        </div>
      )}
    </div>
  );
}

function App() {
  const [api, setApi] = useState(null);
  const [username, setUsername] = useState('');
  const [nodeId, setNodeId] = useState('');

  const handleConnect = (apiInstance, user, node) => {
    setApi(apiInstance);
    setUsername(user);
    setNodeId(node);
  };

  const handleDisconnect = () => {
    setApi(null);
    setUsername('');
    setNodeId('');
  };

  return (
    <div className="app-container">
      {!api ? (
        <ConnectionScreen onConnect={handleConnect} />
      ) : (
        <ChatInterface 
          api={api} 
          username={username} 
          nodeId={nodeId} 
          onDisconnect={handleDisconnect} 
        />
      )}
    </div>
  );
}

export default App;
