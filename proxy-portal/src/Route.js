import './Route.css';
import axios from "axios";
import React, {useState} from "react";

function ValidateIP(ipaddress) {
  if (/^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$/.test(ipaddress)) {
    return true
  }
  return false
}

function checkIPRoute(ip) {
  return axios.get("/api/test", {
    params: {
      ip: ip
    }
  }).then(r => {
    if (r.data !== "unknown") {
      return Promise.resolve(r.data)
    } else {
      return axios.get("/api/route/", {
        params: {
          target: ip,
        }
      }).then(res => {
        return res.data
      })
    }
  })
}

function checkDomainRoute(domain) {
  return axios.get("/api/route/", {
    params: {
      target: domain,
    }
  }).then(res => {
    return res.data
  })
}

function resolve(domain) {
  return axios.get("/api/dns/", {
    params: {
      question: domain,
    }
  }).then(r => {
    if (r.data.error === undefined) {
      if (r.data.answer.Answer !== null) {
        return {
          resolver: r.data.resolver,
          answer: r.data.answer.Answer.map(obj => obj.A).filter(it => it != null)
        }
      } else {
        return {
          resolver: r.data.resolver,
          answer: [],
          cause: "NO ANSWER"
        }
      }
    } else {
      return {
        resolver: r.data.resolver,
        answer: [],
        cause: r.data.error
      }
    }
  })
}

function Route() {
  const [val, setVal] = useState("")
  const [content, setContent] = useState(undefined)
  let question = function (queryArg) {
    if (ValidateIP(queryArg)) {
      checkIPRoute(queryArg).then(r => {
        setContent({
          target: queryArg,
          type: "ip",
          route: r
        })
      })
    } else {
      Promise.all([resolve(queryArg), checkDomainRoute(queryArg)])
        .then(([dns, domainRoute]) => {
            console.log(domainRoute)
            if (dns.answer.length > 0) {
              Promise.all(
                dns.answer.map(ip => checkIPRoute(ip)
                  .then(route => ({
                    ip: ip,
                    route: route
                  }))
                ))
                .then(routes =>
                  setContent({
                    target: queryArg,
                    type: "domain",
                    route: domainRoute,
                    resolver: dns.resolver,
                    answer: routes
                  })
                )
            }
          }
        )
    }
  }
  let contentDiv
  if (content === undefined) {
    contentDiv = (<></>)
  } else {
    if (content.type === "ip") {
      contentDiv = <div className="tableBox">
        <table>
          <tr>
            <th>Target</th>
            <td>{content.target}</td>
          </tr>
          <tr>
            <th>Route</th>
            <td>{content.route}</td>
          </tr>
        </table>
      </div>;
    } else {
      contentDiv = <div className="tableBox">
        <table>
          <tr>
            <th>Target</th>
            <td>{content.target}</td>
          </tr>
          <tr>
            <th>Resolver</th>
            <td>{content.resolver}</td>
          </tr>
          <tr>
            <th>Route</th>
            <td>{content.route}</td>
          </tr>
          <tr>
            <th>Answer</th>
            <td>{content.answer.map(route => <span>{route.ip} → {route.route}</span>)}</td>
          </tr>
        </table>
      </div>;
    }
  }
  return (
    <div className={"ns"} onKeyDown={event => {
      if (event.key === "Enter") {
        question(val)
      }
    }}>
      <div>
        <input value={val} onChange={event => {
          setVal(event.target.value)
        }}/>
        <button onClick={event => {
          question(val)
        }}>
          Check!
        </button>
      </div>
      {contentDiv}
    </div>
  );
}

export default Route;
