"""Integration smoke test against a RUNNING app and your Neon development branch.
Creates disposable test accounts/groups (does not delete any existing data).
Run: python scripts/smoke_test.py
Optional: BASE_URL=http://localhost:8080 ORIGIN=http://localhost:3000
"""
import concurrent.futures
import http.cookiejar
import json
import os
import time
import urllib.error
import urllib.request
import uuid

BASE = os.getenv('BASE_URL', 'http://localhost:3000') + '/api'
ORIGIN = os.getenv('ORIGIN', 'http://localhost:3000')

class Client:
    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
    def call(self, method, path, body=None, key=None, expected=200, origin=ORIGIN):
        headers = {'Content-Type':'application/json','Origin':origin,'X-TripLedger':'1'}
        if key: headers['Idempotency-Key'] = key
        req = urllib.request.Request(BASE+path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=30) as r: status, data = r.status, json.load(r)
        except urllib.error.HTTPError as e:
            status, data = e.code, json.load(e)
        allowed = [expected] if isinstance(expected,int) else expected
        assert status in allowed, (method,path,status,data)
        return data

def main():
    run = uuid.uuid4().hex[:12]
    a,b,c = Client(),Client(),Client()
    users=[]
    for client,name in [(a,'Shivam'),(b,'Aman'),(c,'Outsider')]:
        users.append(client.call('POST','/auth/register',{'name':name,'email':f'{name.lower()}-{run}@example.com','password':'Test-passphrase-123!'},expected=201))
    u,v,_=users
    g=a.call('POST','/groups',{'name':f'Smoke test {run}'},expected=201)['id']
    base=f'/groups/{g}'
    c.call('GET',base+'/dashboard',expected=403)
    a.call('POST','/groups',{'name':'Blocked CSRF'},origin='https://evil.example',expected=403)
    invite=a.call('POST',base+'/invites',{},expected=201)['url'].split('invite=')[1]
    b.call('POST','/invites/accept',{'token':invite})
    b.call('POST',base+'/invites',{},expected=403)
    expense={'description':'Dinner','amount_paise':90000,'paid_by':u['id'],'participant_ids':[u['id'],v['id']],'version':0}
    key=str(uuid.uuid4())
    first=a.call('POST',base+'/expenses',expense,key)
    repeat=a.call('POST',base+'/expenses',expense,key)
    assert first['id']==repeat['id']
    a.call('POST',base+'/expenses',{**expense,'amount_paise':100},key,expected=409)
    a.call('POST',base+'/expenses',{**expense,'participant_ids':[u['id'],u['id']]},str(uuid.uuid4()),expected=400)
    a.call('POST',base+'/expenses',{**expense,'participant_ids':[users[2]['id']]},str(uuid.uuid4()),expected=400)
    def balances():
        d=a.call('GET',base+'/dashboard')
        return d,{m['id']:m['balance_paise'] for m in d['members']}
    d,bal=balances()
    assert len(d['expenses'])==1 and d['total_paise']==90000 and bal[u['id']]==45000 and bal[v['id']]==-45000, d
    s=b.call('POST',base+'/settlements',{'to_user_id':u['id'],'amount_paise':45000},str(uuid.uuid4()),expected=201)
    assert balances()[1]==bal, 'Pending repayment changed balance'
    b.call('POST',base+f"/settlements/{s['id']}/action",{'action':'confirm'},expected=403)
    a.call('POST',base+f"/settlements/{s['id']}/action",{'action':'confirm'})
    a.call('POST',base+f"/settlements/{s['id']}/action",{'action':'confirm'})
    d,bal=balances();assert sum(bal.values())==0 and all(x==0 for x in bal.values()) and d['total_paise']==90000
    # Two clients sharing the same authenticated session race an edit at version 1.
    race=Client()
    for cookie in a.jar: race.jar.set_cookie(cookie)
    edit={**expense,'amount_paise':100000,'version':1}
    def attempt(client):
        return client.call('PATCH',base+f"/expenses/{first['id']}",edit,str(uuid.uuid4()),expected=[200,409])
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        outcomes=list(pool.map(attempt,[a,race]))
    assert sum('error' in x for x in outcomes)==1,outcomes
    d,bal=balances();assert d['total_paise']==100000 and bal[u['id']]==5000 and bal[v['id']]==-5000,d
    assert d['expenses'][0]['version']==2
    a.call('POST','/auth/logout',{})
    a.call('GET','/me',expected=401)
    print('PASS: accounts, origin checks, membership, invitations, exact splits, idempotency, pending/confirmed repayments, concurrent edits, logout.')
    print('Test group retained in your development database:',g)

if __name__=='__main__': main()
