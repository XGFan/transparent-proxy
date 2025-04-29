import './Ipset.css';
import axios from "axios";
import {useEffect, useState} from "react";


function Ipset() {
  let [ipsets, setIpsets] = useState([]);
  let [ip, setIp] = useState('');
  let [status, setStatus] = useState(0);
  let [freshClickable, setFreshClickable] = useState(true);
  let updateOverall = r => {
    setIpsets(r.data.sets)
    setIp(r.data.ip)
    setStatus(r.data.status)
  };

  useEffect(() => {
    axios.get("/api/status").then(updateOverall)
  }, [setIpsets])
  let removeIp = function (set, ip) {
    console.log("delete", ip, "from", set)
    axios.post("/api/remove", {
      ip: ip,
      set: set
    })
      .then(r => axios.get("/api/status"))
      .then(updateOverall)
  }
  let addIp = function (set, ip) {
    console.log("add", ip, "to", set)
    axios.post("/api/add", {
      ip: ip,
      set: set
    })
      .then(r => axios.get("/api/status"))
      .then(updateOverall)
  }
  let refreshCHN = function () {
    setFreshClickable(false)
    axios.post("/api/refresh-route")
      .then(r => {
        if (r.status === 200) {
          alert("Refresh Success")
        }
      })
      .finally(() => {
        setFreshClickable(true)
      })
  }
  let syncNFT = function () {
    axios.post("/api/sync")
        .then(r => {
          if (r.status === 200) {
            alert("Sync Success")
          }
        })
        .finally(() => {
        })
  }

  let cards = ipsets.map(it => {
    return (<div className="card" key={it.name}>
      <header className="fix-header">{it.name}</header>
      <div className="content">
        <div className="ips">
          {
            (it.elems ?? []).map(e => (<span key={e} onClick={function (event) {
              removeIp(it.name, e)
            }}>{e}</span>))
          }
          <span><input onKeyDown={function (event) {
            if (event.key === 'Enter') {
              addIp(it.name, event.target.value)
              event.target.value = ''
            }
          }}/></span>
        </div>
      </div>
    </div>)
  });
  return (
    <div className="ipset">
      <div className="operation">
        <div>
          <span>Transparent: <b>{status === 0 ? 'Disabled' : 'Enabled'}</b></span>
          <br/>
          <span>Your IP: <b>{ip}</b></span>
        </div>
        <button disabled={!freshClickable} onClick={refreshCHN}>Update CHNRoute</button>
        <button onClick={syncNFT}>Sync</button>
      </div>
      <div className="container">
        {cards}
      </div>
    </div>
  );
}

export default Ipset;
