// 등록 폼 Island: 서버가 그린 등록 폼(#join-form)의 제출만 담당한다.
// - 기존 POST /api/parties에 JSON으로 요청하고, 성공하면 일반 페이지 이동으로 /status에 간다.
// - 브라우저 검사는 편의용이다. 최종 판단은 서버 검증이다.
// - 식별 쿠키는 브라우저가 처리한다. 이 스크립트는 토큰을 읽거나 저장하지 않는다.
(() => {
  'use strict';

  const form = document.getElementById('join-form');
  if (!form) return;

  const button = form.querySelector('button[type="submit"]');
  const formError = document.getElementById('form-error');
  const fields = {
    name: form.elements.namedItem('name'),
    email: form.elements.namedItem('email'),
    partySize: form.elements.namedItem('partySize'),
  };
  const LABEL = '대기 등록하기';
  const BUSY = '등록하는 중…';
  let submitting = false;

  // JavaScript가 준비된 뒤에만 제출할 수 있다. (서버가 그린 버튼은 disabled 상태)
  button.disabled = false;

  function setFieldError(name, message) {
    const input = fields[name];
    const box = document.getElementById(name + '-error');
    if (!input || !box) return;
    if (message) {
      box.textContent = message;
      box.hidden = false;
      input.setAttribute('aria-invalid', 'true');
    } else {
      box.textContent = '';
      box.hidden = true;
      input.removeAttribute('aria-invalid');
    }
  }

  function setFormError(message) {
    formError.textContent = message || '';
    formError.hidden = !message;
  }

  function clearErrors() {
    Object.keys(fields).forEach((name) => setFieldError(name, ''));
    setFormError('');
  }

  function focusFirstInvalid() {
    const first = Object.values(fields).find((input) => input.getAttribute('aria-invalid') === 'true');
    if (first) first.focus();
  }

  // 브라우저 입력 검사 (서버와 같은 안내 문구)
  function localErrors() {
    const errors = {};
    if (fields.name.value.trim() === '') errors.name = '이름을 입력해 주세요.';
    const email = fields.email;
    if (email.value.trim() === '') errors.email = '이메일을 입력해 주세요.';
    else if (email.validity.typeMismatch) errors.email = '올바른 이메일 형식이 아닙니다.';
    const size = fields.partySize;
    if (size.validity.badInput || size.value.trim() === '') errors.partySize = '인원수를 입력해 주세요.';
    else if (!size.validity.valid) errors.partySize = '인원수는 1~10 사이의 정수로 입력해 주세요.';
    return errors;
  }

  function partySizeValue(raw) {
    const t = raw.trim();
    // 정수 모양이면 숫자로, 아니면 문자열 그대로 보내 서버가 판단하게 한다.
    return /^\d+$/.test(t) ? Number(t) : t;
  }

  function setBusy(busy) {
    submitting = busy;
    button.disabled = busy;
    button.textContent = busy ? BUSY : LABEL;
    if (busy) form.setAttribute('aria-busy', 'true');
    else form.removeAttribute('aria-busy');
  }

  Object.keys(fields).forEach((name) => {
    fields[name].addEventListener('input', () => setFieldError(name, ''));
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (submitting) return; // 중복 제출 방지

    clearErrors();
    const local = localErrors();
    if (Object.keys(local).length > 0) {
      Object.entries(local).forEach(([name, message]) => setFieldError(name, message));
      focusFirstInvalid();
      return;
    }

    setBusy(true);
    let res;
    let body = null;
    try {
      res = await fetch('/api/parties', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({
          name: fields.name.value,
          email: fields.email.value,
          partySize: partySizeValue(fields.partySize.value),
        }),
        cache: 'no-store',
      });
      body = await res.json().catch(() => null);
    } catch (e) {
      setBusy(false);
      setFormError('서버에 연결할 수 없습니다. 네트워크 연결을 확인한 뒤 다시 시도해 주세요.');
      return;
    }

    if (res.status === 201 || res.status === 200) {
      // 새 등록이거나, 이 브라우저의 기존 진행 중 등록이다. 일반 페이지 이동으로 상태 화면에 간다.
      window.location.assign('/status');
      return;
    }

    setBusy(false);
    const error = (body && body.error) || {};
    if (res.status === 400 && error.fields) {
      Object.entries(error.fields).forEach(([name, message]) => setFieldError(name, message));
      setFormError(error.message || '입력값을 확인해 주세요.');
      focusFirstInvalid();
    } else if (res.status === 409) {
      setFieldError('email', error.message || '이 이메일로 진행 중인 등록이 있습니다.');
      focusFirstInvalid();
    } else if (res.status === 403) {
      setFormError('보안 확인에 실패했습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.');
    } else if (res.status === 400) {
      setFormError('요청을 처리하지 못했습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.');
    } else {
      setFormError('일시적인 오류로 등록하지 못했습니다. 잠시 후 다시 시도해 주세요.');
    }
  });
})();
