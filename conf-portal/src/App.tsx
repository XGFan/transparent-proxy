import MonacoEditor from '@uiw/react-monacoeditor';
import 'normalize.css'
import './App.css'
import axios from "axios";
import {MouseEventHandler, useEffect, useState} from "react";

interface Action {
  pre: string
  post: string
}

interface ConfigItem {
  name: string,
  description: string,
  fileType: string,
  location: string,
  action: Action
}

class Content {
  name: string;
  location: string;
  content: string;
  fileType: string;
  readonly: boolean = true

  constructor(name: string, location: string, content: string, fileType: string) {
    this.name = name;
    this.location = location;
    this.content = content;
    this.fileType = fileType;
  }
}

export function App() {
  const [items, setItems] = useState<ConfigItem[]>([]);
  const [confContent, setConfContent] = useState<Content | null>(null);
  const [temp, setTemp] = useState<String>("");

  useEffect(() => {
    axios.get("/api/conf")
      .then(res => {
        setItems(res.data as ConfigItem[])
      })
  }, [])

  function ClickAction(item: ConfigItem): MouseEventHandler<HTMLLIElement> {
    return event => {
      axios.get("/api/conf/" + item.name + "/content", {
        transformResponse: (data, headers) => {
          return data
        },
        responseType: "text"
      })
        .then(res => {
          const content = new Content(
            item.name,
            item.location,
            res.data as string,
            item.fileType
          );
          console.log('fetch new data: ', content)
          setConfContent(content)
        })
    }
  }

  function edit() {
    console.log("before click edit: ", confContent)
    const content = {
      ...confContent,
      readonly: false
    } as Content
    console.log("after click edit: ", content)
    setConfContent(content)
  }

  function save() {
    axios.post(`/api/conf/${confContent?.name}/content`, temp)
      .then(res => {
        console.log(res)
      })
  }

  return (<div className={'container'}>
    <div className={'sideBar'}>
      <ul>
        {
          items.map(value => {
            return <li className={"file"} key={value.name} onClick={ClickAction(value)}>{value.name}</li>
          })
        }
      </ul>
    </div>
    <div className={'content'}>
      <div className={'titleBar'}>
        <span>{confContent?.location}</span>
        {confContent?.readonly ?
          <img id={'edit'} src={'edit.svg'} alt={'edit'} onClick={edit}/> :
          <img id={'save'} src={'save.svg'} alt={'save'} onClick={save}/>}
      </div>
      <div className={'editor'}>
        <MonacoEditor
          className={'editor'}
          language={confContent?.fileType}
          value={confContent?.content}
          onChange={setTemp}
          options={{
            theme: 'vs-dark',
            readOnly: confContent?.readonly,
          }}
        />
      </div>
    </div>
  </div>)
}