// 로컬 학습용 단일 노드 replica set 초기화 스크립트 (mongosh로 실행).
//
// - 이미 초기화돼 있으면 아무것도 바꾸지 않는다. 따라서 여러 번 실행해도 안전하다.
// - 멤버 호스트는 Compose 서비스 이름(mongo)으로 광고한다.
//   앱 컨테이너는 같은 Compose 네트워크에서 이 이름을 해석할 수 있다.
// - 단일 노드이므로 고가용성 구성이 아니다. 트랜잭션 사용을 위한 구성이다.

const setName = process.env.MONGO_REPLICA_SET_NAME || "rs0";
const memberHost = process.env.MONGO_MEMBER_HOST || "mongo:27017";

function isNotYetInitialized(e) {
  return e.code === 94 || e.codeName === "NotYetInitialized";
}

try {
  const status = rs.status();
  print(`replica set already initialized: ${status.set}`);
} catch (e) {
  if (!isNotYetInitialized(e)) {
    throw e;
  }
  rs.initiate({ _id: setName, members: [{ _id: 0, host: memberHost }] });
  print(`replica set initiated: ${setName} (${memberHost})`);
}

// 저장된 설정이 기대값과 다르면(예: 예전 볼륨) 바꾸지 않고 실패로 알린다.
const conf = rs.conf();
const members = conf.members.map((m) => m.host);
if (conf._id !== setName || members.length !== 1 || members[0] !== memberHost) {
  throw new Error(`unexpected replica set config: ${conf._id} [${members.join(", ")}]`);
}

// primary가 선출될 때까지 기다린다.
const deadline = Date.now() + 60 * 1000;
while (!db.hello().isWritablePrimary) {
  if (Date.now() > deadline) {
    throw new Error("timed out waiting for primary");
  }
  sleep(500);
}
print("replica set primary is ready");
