import React from 'react';
import ReactDOM from 'react-dom';
import './index.css';
import App from './App';
import Dns from "./Dns";

ReactDOM.render(
  <React.StrictMode>
    <Dns/>
    <App/>
  </React.StrictMode>,
  document.getElementById('root')
);
