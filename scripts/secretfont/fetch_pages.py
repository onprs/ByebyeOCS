#!/usr/bin/env python3
"""并发抓取加密测验页面（维护期只读）。

默认从本地维护库读取待抓章节，页面保存在 .local/probe/pages，跳过已存在页面。
只用于维护环境补字形索引，不属于正式服务依赖。
"""
import json, subprocess, sys, threading, time, urllib.parse, urllib.request, urllib.error
from pathlib import Path
ROOT = Path(".").resolve(); sys.path.insert(0, str(ROOT/"chaoxing-progress-research"/"tools"))
import login_account as la

OUT = Path(".local/probe/pages"); OUT.mkdir(parents=True, exist_ok=True)
rows = subprocess.run(["docker","exec","byebyeocs-full-db","psql","-U","byebyeocs_full","-d","byebyeocs_full","-At","-F","\t","-c",
 "select c.external_course_id, c.external_class_id, c.external_cpi, t.chapter_external_id, a.card_index, t.task_point_id from learning_tasks t join learning_courses c on c.id=t.course_id join learning_attachments a on a.id=t.attachment_id where t.task_kind='quiz' order by c.title, t.chapter_external_id;"],
 capture_output=True, text=True).stdout.strip().splitlines()
todo = [line for line in rows if not (OUT / ("quiz-%s.html" % line.split("\t")[3])).exists()]
print("待抓取", len(todo), flush=True)
lock = threading.Lock(); saved = [0]

def worker():
    env = la.read_environment(ROOT/".env")
    jar = None
    for _ in range(6):
        try:
            status, jar, _ = la.login(env); break
        except Exception:
            time.sleep(2)
    if jar is None:
        return
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    def get(url, ref):
        for _ in range(3):
            try:
                code, body = la.open_json(opener, urllib.request.Request(url, headers={"User-Agent": la.USER_AGENT, "Referer": ref}))
                return code, body
            except urllib.error.HTTPError as error:
                return error.code, error.read()
            except Exception:
                time.sleep(2)
        return 0, b""
    while True:
        with lock:
            if not todo:
                return
            courseid, clazzid, cpi, knowledge, index, jobid = todo.pop().split("\t")
        path = OUT / ("quiz-%s.html" % knowledge)
        if path.exists():
            continue
        q = {"clazzid": clazzid, "courseid": courseid, "knowledgeid": knowledge, "num": index, "ut": "s", "cpi": cpi, "v": "20190915", "mooc2": "1"}
        _, body = get(la.CARDS_BASE + "?" + urllib.parse.urlencode(q), "https://mooc2-ans.chaoxing.com/")
        text = body.decode("utf-8", "replace"); start = text.find('"attachments"')
        if start < 0:
            continue
        try:
            card, _ = json.JSONDecoder().raw_decode(text[text.rfind("{", 0, start):])
            item = next(x for x in card["attachments"] if str(x.get("jobid")) == jobid); defaults = card["defaults"]
        except Exception:
            continue
        prop = item.get("property") or {}
        wq = {"api":"1","workId":jobid[5:],"jobid":jobid,"originJobId":jobid,"needRedirect":"true","skipHeader":"true",
              "knowledgeid":knowledge,"ktoken":defaults.get("ktoken") or item.get("ktoken") or prop.get("ktoken") or "",
              "cpi":cpi,"ut":"s","clazzId":clazzid,"type":"","enc":item.get("enc") or prop.get("enc") or "","mooc2":"1","courseid":courseid}
        code, page = get("https://mooc1.chaoxing.com/mooc-ans/api/work?" + urllib.parse.urlencode(wq), "https://mooc1.chaoxing.com/mooc-ans/knowledge/cards")
        body_text = page.decode("utf-8", "replace")
        if code != 200 or "cxSecretStyle" not in body_text or 'class="TiMu' not in body_text:
            continue
        path.write_text(body_text, encoding="utf-8")
        with lock:
            saved[0] += 1
            if saved[0] % 20 == 0:
                print("已保存", saved[0], flush=True)
        time.sleep(0.3)

threads = [threading.Thread(target=worker, daemon=True) for _ in range(4)]
for thread in threads: thread.start()
for thread in threads: thread.join()
print("完成，保存", saved[0], flush=True)
