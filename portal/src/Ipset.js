import './Ipset.css';
import axios from "axios";
import {useEffect, useState} from "react";


function Ipset() {
  let [ipsets, setIpsets] = useState([]);
  let [ip, setIp] = useState('');
  let [v2ConfStr, setV2ConfStr] = useState('');
  let [status, setStatus] = useState(0);
  let [freshClickable, setFreshClickable] = useState(true);
  let updateOverall = r => {
    setIpsets(r.data.sets)
    setIp(r.data.ip)
    setStatus(r.data.status)
  };
  const getV2 = () => {
    axios.get("/api/v2-conf")
      .then(r => {
        setV2ConfStr(JSON.stringify(r.data, null, 4))
      })
  }
  useEffect(getV2, []);
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
  let applyV2 = function (e) {
    try {
      // 尝试解析 JSON 字符串并重新格式化
      const json = JSON.parse(v2ConfStr);
      // setJsonString(JSON.stringify(json, null, 2));
      axios.post("/api/v2-conf", json, {})
        .then(value => {
          console.log(value)
        })
        .then(value => getV2())
    } catch (error) {
      // 如果 JSON 无效，可以在这里处理错误
      console.error("Invalid JSON:", v2ConfStr);
    }
  }

  let handleChange = function (event) {
    setV2ConfStr(event.target.value);
  }

  let cards = ipsets.map(it => {
    return (<div className="card" key={it.name}>
      <header className="fix-header">{it.name}</header>
      <div className="content">
        <div className="ips">
          {
            (it.ip ?? []).map(e => (<span key={e} onClick={function (event) {
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
      <div style={{marginTop: '1rem'}}>
        <div className="card">
          <header className="fix-header">v2ray config</header>
          <div className="content">
            <textarea style={{backgroundColor: '#eeeeee', width: '100%', height: '24rem'}}
                      value={v2ConfStr}
                      onChange={handleChange}/>
            <div style={{height: '1.5rem'}}>
              <button style={{float: 'right', height: '1.5rem'}} onClick={applyV2}>Apply</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export default Ipset;
