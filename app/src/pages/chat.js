import { useState, useEffect } from 'react';
import io from 'socket.io-client';

export default function Home() {
  const [socket, setSocket] = useState(null);
  const [message, setMessage] = useState('');
  const [response, setResponse] = useState('');
  const [currentTime, setCurrentTime] = useState('');
  const [output, setOutput] = useState('');

  useEffect(() => {
    const socketInitializer = async () => {
      await fetch('/api/socket');
      const newSocket = io();

      newSocket.on('connect', () => {
        console.error('Connected to Socket.IO server');
      });

      newSocket.on('echo', (msg) => {
        setResponse(msg);
      });

      newSocket.on('time', (time) => {
        setCurrentTime(time);
      });

      newSocket.on('connect_error', (error) => {
        console.error('Connection error:', error);
      });

      newSocket.on('commandOutput', (data) => {
        setOutput((prevOutput) => prevOutput + data);
      });

      setSocket(newSocket);

      return () => {
        newSocket.disconnect();
      };
    };

    socketInitializer();
  }, []);

  const sendMessage = () => {
    if (socket && message) {
      socket.emit('message', message);
      setMessage('');
    } else {
      console.error('Socket or message not available');
    }
  };

  return (
    <div>
      <h1>Socket.IO Message Echo Demo with Time Updates</h1>
      <input type='text' value={message} onChange={(e) => setMessage(e.target.value)} placeholder='Enter a message' />
      <button onClick={sendMessage}>Send Message</button>
      <p>Server Response: {response}</p>
      <p>Current Server Time: {currentTime}</p>
      <pre>{output}</pre>
    </div>
  );
}
