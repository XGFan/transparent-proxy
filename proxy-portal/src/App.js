import './App.css';
import axios from "axios";
import {useEffect, useState} from "react";


function App() {
  let [ipsets, setIpsets] = useState([]);
  useEffect(() => {
    axios.get("/api/status").then(r => setIpsets(r.data.sets))
  }, [setIpsets])
  let removeIp = function (set, ip) {
    console.log("delete", ip, "from", set)
    axios.delete("/api/remove", {
      data: {
        ip: ip,
        set: set
      }
    })
      .then(r => axios.get("/api/"))
      .then(r => setIpsets(r.data.sets))
  }

  let addIp = function (set, ip) {
    console.log("add", ip, "to", set)
    axios.delete("/api/add", {
      data: {
        ip: ip,
        set: set
      }
    })
      .then(r => axios.get("/api/"))
      .then(r => setIpsets(r.data.sets))
  }
  let cards = ipsets.map(it => {
    return (<div className="card" key={it.Name}>
      <header className="fix-header">{it.Name}</header>
      <div className="content">
        <table>
          <tbody>
          {/*<tr>*/}
          {/*  <td>Type</td>*/}
          {/*  <td>{it.SetType}</td>*/}
          {/*</tr>*/}
          {/*<tr>*/}
          {/*  <td>Header</td>*/}
          {/*  <td>{it.Header}</td>*/}
          {/*</tr>*/}
          <tr>
            <td>References</td>
            <td>{it.References}</td>
          </tr>
          <tr>
            <td>Size In Memory</td>
            <td>{it.SizeInMemory}</td>
          </tr>
          </tbody>
        </table>
        <div className="ips">
          {
            (it.Entries ?? []).map(e => (<span key={e} onClick={function (event) {
              removeIp(it.Name, e)
            }}>{e}</span>))
          }
          <span><input onKeyDown={function (event) {
            if (event.key === 'Enter') {
              addIp(it.Name, event.target.value)
              event.target.value = ''
            }
          }}/></span>
        </div>
      </div>
    </div>)
  });
  return (
    <div className="container">
      {cards}
    </div>
  );
}

export default App;
