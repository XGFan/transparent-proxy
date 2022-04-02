import './Dns.css';
import axios from "axios";
import React, {useState} from "react";


function Dns() {
  const [val, setVal] = useState("")
  const [content, setContent] = useState(undefined)
  let question = function (question) {
    axios.get("/api/dns/", {
      params: {
        question: question,
      }
    })
      .then(r => {
        if (r.data.error === undefined) {
          setContent({
            resolver: r.data.resolver,
            answer: r.data.answer.Answer.map(obj => obj.A).filter(it => it != null)
          })
        }
      })
  }
  let contentDiv
  if (content != undefined) {
    contentDiv = <>
      <div>
        <p>Resolver: {content.resolver}</p>
        <p>Answer: {content.answer}</p>
      </div>
    </>;
  } else {
    contentDiv = (<></>)
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
          nslookup
        </button>
      </div>
      {contentDiv}
    </div>
  );
}

export default Dns;
