import { inject, provide, ref, type InjectionKey, type Ref } from "vue";

/**
 * Whether the person looking at a page may change things in it. The operator's console never provides this (everything is allowed there: the operator
 * token is the credential); the product's tenant layout provides it from the person's role in the account that owns the tenant, so a page shared by both
 * can switch its forms off for a viewer instead of offering buttons the server would refuse. The server is what enforces it: a viewer's session may only GET.
 */
const CAN_WRITE: InjectionKey<Ref<boolean>> = Symbol("canWrite");

export function provideCanWrite(value: Ref<boolean>): void {
  provide(CAN_WRITE, value);
}

export function useCanWrite(): Ref<boolean> {
  return inject(CAN_WRITE, ref(true));
}
