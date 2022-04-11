import React from 'react';
import ReactDOM from 'react-dom';
import './index.css';
import Ipset from "./Ipset";
import Route from "./Route";

ReactDOM.render(
  <React.StrictMode>
    <Route/>
    <Ipset/>
  </React.StrictMode>,
  document.getElementById('root')
);
