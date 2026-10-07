module Editing exposing (Model, Msg(..), init, isOpen, keptDuration, update, view)

import Browser.Dom
import Button
import Card
import Dialog
import Html exposing (Html, div, input, label, p, text)
import Html.Attributes exposing (attribute, autofocus, checked, class, disabled, id, name, type_)
import Html.Events exposing (on, onClick, preventDefaultOn)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Process
import Task
import Ui
import Url


type alias Breakpoint =
    { id : String, time : Float, kind : String, edited : Bool, sourceTime : Maybe Float }


type alias Section =
    { id : String, keep : Bool }


type alias Draft =
    { duration : Float, revision : Int, breakpoints : List Breakpoint, sections : List Section }


type Selection
    = Boundary Int
    | Passage Int


type alias Model =
    { sermonId : Maybe String
    , visible : Bool
    , draft : Maybe Draft
    , selection : Maybe Selection
    , saved : Maybe Draft
    , saving : Bool
    , regenerating : Bool
    , applying : Bool
    , pendingApply : Maybe Bool
    , error : Maybe String
    , audioStatus : String
    , playhead : Float
    , undo : List Draft
    , generation : Int
    , sequence : Int
    , confirmingRegenerate : Bool
    , showingDurationDialog : Bool
    , preserveEdited : Bool
    , addingBreakpoint : Bool
    }


init : Model
init =
    Model Nothing False Nothing Nothing Nothing False False False Nothing Nothing "" 0 [] 0 0 False False True False


type Msg
    = Open String
    | ApplyRecording String
    | Loaded Int (Result Http.Error Draft)
    | Close
    | Select Selection
    | Keep Int Bool
    | Nudge Float
    | ToggleAddingBoundary
    | PlaceBoundary Float
    | AddBoundary
    | RemoveBoundary
    | Undo
    | SaveLater Int
    | RetrySave
    | Saved Int (Result Http.Error Draft)
    | Apply Bool
    | Applied Int (Result Http.Error ())
    | Reload
    | Regenerate
    | ConfirmRegenerate
    | CancelRegenerate
    | DismissDurationDialog
    | ChooseRegenerateMode Bool
    | Regenerated Int (Result Http.Error Draft)
    | Preview
    | PlayFull
    | Playhead Float
    | AudioStatus String
    | Focused (Result Browser.Dom.Error ())
    | IgnoreClick


isOpen : String -> Model -> Bool
isOpen sermonId model =
    model.visible && model.sermonId == Just sermonId


endpoint : Model -> String
endpoint model =
    "/api/sermons/" ++ Url.percentEncode (Maybe.withDefault "" model.sermonId) ++ "/editing"


draftDecoder : Decode.Decoder Draft
draftDecoder =
    Decode.map4 Draft
        (Decode.field "duration" Decode.float)
        (Decode.field "revision" Decode.int)
        (Decode.field "breakpoints" (Decode.list (Decode.map5 Breakpoint (Decode.field "id" Decode.string) (Decode.field "time" Decode.float) (Decode.field "kind" Decode.string) (Decode.oneOf [ Decode.field "edited" Decode.bool, Decode.succeed False ]) (Decode.maybe (Decode.field "source_time" Decode.float)))))
        (Decode.field "sections" (Decode.list (Decode.map2 Section (Decode.field "id" Decode.string) (Decode.field "keep" Decode.bool))))


encodeDraft : Draft -> Encode.Value
encodeDraft draft =
    Encode.object
        [ ( "duration", Encode.float draft.duration )
        , ( "revision", Encode.int draft.revision )
        , ( "breakpoints", Encode.list (\b -> Encode.object [ ( "id", Encode.string b.id ), ( "time", Encode.float b.time ), ( "kind", Encode.string b.kind ), ( "edited", Encode.bool b.edited ), ( "source_time", Maybe.map Encode.float b.sourceTime |> Maybe.withDefault Encode.null ) ]) draft.breakpoints )
        , ( "sections", Encode.list (\s -> Encode.object [ ( "id", Encode.string s.id ), ( "keep", Encode.bool s.keep ) ]) draft.sections )
        ]


effect : String -> List ( String, Encode.Value ) -> Maybe Encode.Value
effect action fields =
    Just (Encode.object (( "action", Encode.string action ) :: fields))


stop : Maybe Encode.Value
stop =
    effect "stop" []


update : Msg -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
update msg model =
    case msg of
        Open sermonId ->
            if model.sermonId == Just sermonId && model.draft /= Nothing then
                ( { model | visible = True }, Cmd.none, stop )

            else
                let
                    next =
                        { init | sermonId = Just sermonId, visible = True, generation = model.generation + 1 }
                in
                ( next, Http.get { url = endpoint next, expect = Http.expectJson (Loaded next.generation) draftDecoder }, stop )

        ApplyRecording sermonId ->
            if model.sermonId == Just sermonId && model.draft /= Nothing then
                update (Apply False) model

            else
                let
                    ( next, cmd, audioEffect ) =
                        update (Open sermonId) model
                in
                ( { next | visible = False, pendingApply = Just False }, cmd, audioEffect )

        Loaded generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok draft ->
                        let
                            next =
                                { model | draft = Just draft, saved = Just draft, error = Nothing, selection = Just (Passage 0) }
                        in
                        case next.pendingApply of
                            Just skip ->
                                apply skip next

                            Nothing ->
                                ( next, Cmd.none, Nothing )

                    Err (Http.BadStatus 404) ->
                        ( { model | error = Just "This recording has no edit draft yet. Close the editor and regenerate its transcription to enable editing.", pendingApply = Nothing }, Cmd.none, Nothing )

                    Err _ ->
                        ( { model | error = Just "Could not load editing suggestions. Try again.", pendingApply = Nothing }, Cmd.none, Nothing )

        Close ->
            ( { model | visible = False, confirmingRegenerate = False, addingBreakpoint = False }, Cmd.none, stop )

        Reload ->
            update (Open (Maybe.withDefault "" model.sermonId)) { model | draft = Nothing }

        Regenerate ->
            ( { model | confirmingRegenerate = True, preserveEdited = True, addingBreakpoint = False }, Cmd.none, stop )

        CancelRegenerate ->
            ( { model | confirmingRegenerate = False }, Task.attempt Focused (Browser.Dom.focus "regenerate-breakpoints"), Nothing )

        ChooseRegenerateMode preserve ->
            ( { model | preserveEdited = preserve }, Cmd.none, Nothing )

        ConfirmRegenerate ->
            case model.draft of
                Just draft ->
                    if model.saving || model.regenerating || model.applying || model.pendingApply /= Nothing || model.draft /= model.saved then
                        ( model, Cmd.none, Nothing )

                    else
                        ( { model | regenerating = True, confirmingRegenerate = False, error = Nothing }
                        , Http.post { url = endpoint model ++ "/regenerate", body = Http.jsonBody (Encode.object [ ( "revision", Encode.int draft.revision ), ( "preserve_edited", Encode.bool model.preserveEdited ) ]), expect = Http.expectJson (Regenerated model.generation) draftDecoder }
                        , stop
                        )

                Nothing ->
                    ( model, Cmd.none, Nothing )

        Regenerated generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok draft ->
                        ( { model | regenerating = False, draft = Just draft, saved = Just draft, selection = Just (Passage 0), undo = [], sequence = model.sequence + 1, audioStatus = "" }, Cmd.none, stop )

                    Err err ->
                        ( { model | regenerating = False, error = Just (saveError err) }, Cmd.none, Nothing )

        Select selection ->
            ( { model
                | selection = Just selection
                , addingBreakpoint = False
                , audioStatus = ""
                , playhead = sectionStart selection model
              }
            , if model.selection == Just selection then
                Cmd.none

              else
                case selection of
                    Boundary _ ->
                        Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment")

                    Passage _ ->
                        Cmd.none
            , stop
            )

        IgnoreClick ->
            ( model, Cmd.none, Nothing )

        Focused _ ->
            ( model, Cmd.none, Nothing )

        Keep index keep ->
            change (\d -> { d | sections = replaceAt index (\s -> { s | keep = keep }) d.sections }) model

        Nudge delta ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Boundary index) ) ->
                    case ( at index draft.breakpoints, at (index - 1) draft.breakpoints, at (index + 1) draft.breakpoints ) of
                        ( Just current, Just before, Just after ) ->
                            let
                                time =
                                    clamp (before.time + 0.05) (after.time - 0.05) (toFloat (round ((current.time + delta) * 1000)) / 1000)

                                ( next, cmd, _ ) =
                                    change (\d -> { d | breakpoints = replaceAt index (\b -> { b | time = time, edited = b.edited || time /= b.time }) d.breakpoints }) model
                            in
                            ( next, cmd, preview next )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        ToggleAddingBoundary ->
            ( { model | addingBreakpoint = not model.addingBreakpoint }
            , if model.addingBreakpoint then
                Cmd.none

              else
                Task.attempt Focused (Browser.Dom.focus "editing-waveform-canvas")
            , stop
            )

        PlaceBoundary time ->
            case model.draft of
                Just draft ->
                    if model.addingBreakpoint then
                        let
                            index =
                                draft.breakpoints
                                    |> List.indexedMap Tuple.pair
                                    |> List.filter (\( _, boundary ) -> boundary.time <= time)
                                    |> List.reverse
                                    |> List.head
                                    |> Maybe.map Tuple.first
                                    |> Maybe.withDefault 0
                                    |> min (List.length draft.sections - 1)
                        in
                        update AddBoundary { model | selection = Just (Passage index), playhead = toFloat (round (time * 1000)) / 1000 }

                    else
                        ( model, Cmd.none, Nothing )

                Nothing ->
                    ( model, Cmd.none, Nothing )

        AddBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Passage index) ) ->
                    case ( at index draft.breakpoints, at (index + 1) draft.breakpoints, at index draft.sections ) of
                        ( Just before, Just after, Just section ) ->
                            if model.playhead - before.time < 0.05 - 0.000000001 || after.time - model.playhead < 0.05 - 0.000000001 then
                                ( { model | error = Just "Breakpoints must be at least 0.05 seconds apart." }, Cmd.none, Nothing )

                            else
                                let
                                    unique =
                                        "manual-" ++ String.fromInt draft.revision ++ "-" ++ String.fromInt model.sequence

                                    ( next, cmd, audioEffect ) =
                                        change
                                            (\d ->
                                                { d
                                                    | breakpoints = List.take (index + 1) d.breakpoints ++ [ Breakpoint unique model.playhead "manual" True Nothing ] ++ List.drop (index + 1) d.breakpoints
                                                    , sections = List.take (index + 1) d.sections ++ [ Section (unique ++ "-section") section.keep ] ++ List.drop (index + 1) d.sections
                                                }
                                            )
                                            model
                                in
                                ( { next | selection = Just (Boundary (index + 1)), addingBreakpoint = False }, Cmd.batch [ cmd, Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment") ], audioEffect )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        RemoveBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Boundary index) ) ->
                    case ( at (index - 1) draft.sections, at index draft.sections ) of
                        ( Just left, Just right ) ->
                            let
                                ( next, cmd, audioEffect ) =
                                    change (\d -> { d | breakpoints = removeAt index d.breakpoints, sections = removeAt index (replaceAt (index - 1) (\section -> { section | keep = left.keep || right.keep }) d.sections) }) model
                            in
                            ( { next | selection = Just (Passage (index - 1)) }, cmd, audioEffect )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        Undo ->
            case ( model.undo, model.draft ) of
                ( previous :: rest, Just draft ) ->
                    let
                        ( next, cmd, audioEffect ) =
                            change (\_ -> { previous | revision = draft.revision }) model
                    in
                    ( { next | undo = rest, selection = Nothing }, cmd, audioEffect )

                _ ->
                    ( model, Cmd.none, Nothing )

        SaveLater sequence ->
            if sequence == model.sequence then
                save model

            else
                ( model, Cmd.none, Nothing )

        RetrySave ->
            save { model | error = Nothing }

        Saved generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok saved ->
                        let
                            next =
                                { model | saving = False, saved = Just saved, draft = Maybe.map (\d -> { d | revision = saved.revision }) model.draft, error = Nothing }
                        in
                        if next.draft /= next.saved then
                            save next

                        else
                            case next.pendingApply of
                                Just skip ->
                                    apply skip next

                                Nothing ->
                                    ( next, Cmd.none, Nothing )

                    Err err ->
                        ( { model | saving = False, pendingApply = Nothing, error = Just (saveError err) }, Cmd.none, Nothing )

        Apply skip ->
            if model.regenerating then
                ( model, Cmd.none, Nothing )

            else if Maybe.withDefault 0 (Maybe.map keptDuration model.draft) >= 90 * 60 then
                ( { model | showingDurationDialog = True }, Cmd.none, Nothing )

            else if model.draft /= model.saved || model.saving then
                save { model | pendingApply = Just skip }

            else
                apply skip model

        DismissDurationDialog ->
            ( { model | showingDurationDialog = False }, Cmd.none, Nothing )

        Applied generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok _ ->
                        ( { init | generation = model.generation + 1 }, Cmd.none, stop )

                    Err err ->
                        ( { model | applying = False, pendingApply = Nothing, error = Just (saveError err) }, Cmd.none, Nothing )

        Preview ->
            ( { model | audioStatus = "Loading preview…" }, Cmd.none, preview model )

        PlayFull ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Passage index) ) ->
                    case ( at index draft.breakpoints, at (index + 1) draft.breakpoints ) of
                        ( Just before, Just after ) ->
                            ( { model | audioStatus = "Playing full section" }, Cmd.none, effect "full" [ ( "start", Encode.float before.time ), ( "end", Encode.float after.time ) ] )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        Playhead time ->
            ( { model | playhead = time }, Cmd.none, Nothing )

        AudioStatus status ->
            ( { model | audioStatus = status }, Cmd.none, Nothing )


saveError : Http.Error -> String
saveError err =
    case err of
        Http.BadStatus 409 ->
            "This draft changed in another window, or processing is running. Reload the saved draft before continuing."

        Http.BadBody message ->
            message

        _ ->
            "Could not save or apply your edits. Your changes are still here. Try again."


change : (Draft -> Draft) -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
change transform model =
    case model.draft of
        Just draft ->
            if model.regenerating || model.applying || model.pendingApply /= Nothing || transform draft == draft then
                ( model, Cmd.none, Nothing )

            else
                let
                    next =
                        { model | draft = Just (transform draft), undo = List.take 50 (draft :: model.undo), sequence = model.sequence + 1, error = Nothing, audioStatus = "" }
                in
                ( next, Task.perform (\_ -> SaveLater next.sequence) (Process.sleep 450), stop )

        Nothing ->
            ( model, Cmd.none, Nothing )


save : Model -> ( Model, Cmd Msg, Maybe Encode.Value )
save model =
    case model.draft of
        Just draft ->
            if model.saving || model.regenerating || model.applying || model.draft == model.saved then
                ( model, Cmd.none, Nothing )

            else
                ( { model | saving = True }
                , Http.request { method = "PUT", headers = [], url = endpoint model, body = Http.jsonBody (encodeDraft draft), expect = Http.expectJson (Saved model.generation) draftDecoder, timeout = Nothing, tracker = Nothing }
                , Nothing
                )

        Nothing ->
            ( model, Cmd.none, Nothing )


apply : Bool -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
apply skip model =
    case model.draft of
        Just draft ->
            if keptDuration draft >= 90 * 60 then
                ( { model | visible = True, showingDurationDialog = True, pendingApply = Nothing }, Cmd.none, Nothing )

            else
                ( { model | visible = False, applying = True, pendingApply = Nothing, error = Nothing }
                , Http.post { url = endpoint model ++ "/apply", body = Http.jsonBody (Encode.object [ ( "revision", Encode.int draft.revision ), ( "skip", Encode.bool skip ) ]), expect = Http.expectStringResponse (Applied model.generation) applyResponse }
                , stop
                )

        Nothing ->
            ( model, Cmd.none, Nothing )


applyResponse : Http.Response String -> Result Http.Error ()
applyResponse response =
    case response of
        Http.GoodStatus_ _ _ ->
            Ok ()

        Http.BadStatus_ metadata body ->
            if metadata.statusCode == 409 then
                Err (Http.BadStatus 409)

            else
                Err (Http.BadBody (Decode.decodeString (Decode.field "error" Decode.string) body |> Result.withDefault "Could not apply edits. Please try again."))

        _ ->
            Err Http.NetworkError


preview : Model -> Maybe Encode.Value
preview model =
    case ( model.draft, model.selection ) of
        ( Just draft, Just (Boundary index) ) ->
            at index draft.breakpoints |> Maybe.andThen (\b -> effect "preview" [ ( "url", Encode.string (endpoint model ++ "/preview?mode=breakpoint&time=" ++ String.fromFloat b.time) ) ])

        ( Just draft, Just (Passage index) ) ->
            case ( at index draft.breakpoints, at (index + 1) draft.breakpoints ) of
                ( Just before, Just after ) ->
                    effect "preview" [ ( "url", Encode.string (endpoint model ++ "/preview?mode=section&start=" ++ String.fromFloat before.time ++ "&end=" ++ String.fromFloat after.time) ) ]

                _ ->
                    Nothing

        _ ->
            Nothing


at : Int -> List a -> Maybe a
at index list =
    if index < 0 then
        Nothing

    else
        List.head (List.drop index list)


replaceAt : Int -> (a -> a) -> List a -> List a
replaceAt index transform =
    List.indexedMap
        (\i item ->
            if i == index then
                transform item

            else
                item
        )


removeAt : Int -> List a -> List a
removeAt index list =
    List.take index list ++ List.drop (index + 1) list


sectionStart : Selection -> Model -> Float
sectionStart selection model =
    let
        index =
            case selection of
                Passage i ->
                    i

                Boundary i ->
                    i
    in
    model.draft |> Maybe.andThen (\d -> at index d.breakpoints) |> Maybe.map .time |> Maybe.withDefault 0


timestamp : Float -> String
timestamp time =
    let
        tenths =
            round (time * 10)

        seconds =
            tenths // 10
    in
    String.fromInt (seconds // 60) ++ ":" ++ String.padLeft 2 '0' (String.fromInt (modBy 60 seconds)) ++ "." ++ String.fromInt (modBy 10 tenths)


kindLabel : String -> String
kindLabel kind =
    case kind of
        "silence_start" ->
            "Silence starts"

        "silence_end" ->
            "Silence ends"

        "speaker" ->
            "New speaker detected"

        "singing_start" ->
            "Singing starts"

        "singing_end" ->
            "Singing ends"

        _ ->
            "Manual breakpoint"


view : Model -> Html Msg
view model =
    let
        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing
    in
    div [ class "editor" ]
        [ Card.viewWithSubtitle
            (text "Edit recording")
            (Maybe.map (\draft -> text ("Kept duration: " ++ timestamp (keptDuration draft))) model.draft)
            [ div [ class "editor__actions" ]
                [ Button.view "button"
                    (Button.regenerate "Regenerate breakpoints")
                    model.regenerating
                    [ onClick Regenerate
                    , id "regenerate-breakpoints"
                    , disabled (model.draft == Nothing || model.saving || busy || model.draft /= model.saved)
                    ]
                , div [ class "editor__finish-actions" ]
                    [ case ( model.sermonId, model.draft ) of
                        ( Just sermonId, Just draft ) ->
                            Button.labeled "Apply" Button.applyEdits busy
                                [ onClick (ApplyRecording sermonId), disabled (busy || keptDuration draft <= 0) ]

                        _ ->
                            text ""
                    , Button.action "ph:x" "Close editor" False [ onClick Close ]
                    ]
                ]
            ]
            [ if model.confirmingRegenerate then
                regenerationDialog model

              else
                text ""
            , if model.showingDurationDialog then
                Dialog.view
                    { id = "duration-limit", title = "Audio too long", onClose = DismissDurationDialog }
                    []
                    [ p [ Ui.panelText ] [ text "Edit audio to under 90 minutes." ] ]
                    [ Button.labeled "Edit audio" Button.editAudio False [ onClick DismissDurationDialog, autofocus True ] ]

              else
                text ""
            , case model.error of
                Just error ->
                    div [ Ui.errorPanel, attribute "role" "alert" ]
                        [ p [] [ text error ]
                        , div [ Ui.sermonActions ]
                            [ if model.draft == Nothing then
                                Button.action "ph:x" "Close editor" False [ onClick Close ]

                              else
                                Button.primaryAction "ph:floppy-disk" "Try saving again" model.saving [ onClick RetrySave, disabled model.saving ]
                            , Button.action "ph:arrow-clockwise" "Reload saved draft" False [ onClick Reload, disabled (model.saving || model.regenerating || model.applying) ]
                            ]
                        ]

                Nothing ->
                    text ""
            , case model.draft of
                Nothing ->
                    p [ Ui.hint ]
                        [ text
                            (if model.error == Nothing then
                                "Loading sections…"

                             else
                                ""
                            )
                        ]

                Just draft ->
                    div []
                        [ waveform model draft
                        , navigation model draft
                        , selectedCard model draft
                        ]
            ]
        ]


regenerationDialog : Model -> Html Msg
regenerationDialog model =
    Dialog.view
        { id = "regenerate", title = "Regenerate breakpoints", onClose = CancelRegenerate }
        []
        [ div [ class "editor__dialog-options", attribute "role" "radiogroup", attribute "aria-label" "Breakpoints to regenerate" ]
            (List.map
                (\( preserve, caption ) ->
                    label [ class "editor__choice" ]
                        [ input [ type_ "radio", name "regenerate-mode", checked (model.preserveEdited == preserve), onClick (ChooseRegenerateMode preserve) ] []
                        , text caption
                        ]
                )
                [ ( False, "All breakpoints" ), ( True, "Only unedited breakpoints" ) ]
            )
        ]
        [ Button.labeled "Cancel" Button.cancelDialog False [ onClick CancelRegenerate, autofocus True ]
        , Button.labeled "Regenerate" Button.confirmRegeneration False [ onClick ConfirmRegenerate ]
        ]


waveform : Model -> Draft -> Html Msg
waveform model draft =
    Html.node "editing-waveform"
        [ class "editor__waveform"
        , attribute "src" (endpoint model ++ "/waveform")
        , attribute "data-draft" (Encode.encode 0 (encodeDraft draft))
        , attribute "data-selection"
            (case model.selection of
                Just (Boundary index) ->
                    "boundary:" ++ String.fromInt index

                Just (Passage index) ->
                    "section:" ++ String.fromInt index

                Nothing ->
                    ""
            )
        , attribute "data-disabled"
            (if model.regenerating || model.applying || model.pendingApply /= Nothing then
                "true"

             else
                "false"
            )
        , attribute "data-placing"
            (if model.addingBreakpoint then
                "true"

             else
                "false"
            )
        , on "waveformadd" (Decode.at [ "detail", "time" ] Decode.float |> Decode.map PlaceBoundary)
        , on "waveformcancel" (Decode.succeed ToggleAddingBoundary)
        , on "waveformselect"
            (Decode.at [ "detail" ]
                (Decode.map2
                    (\kind index ->
                        Select
                            (if kind == "boundary" then
                                Boundary index

                             else
                                Passage index
                            )
                    )
                    (Decode.field "kind" Decode.string)
                    (Decode.field "index" Decode.int)
                )
            )
        ]
        []


keptDuration : Draft -> Float
keptDuration draft =
    List.indexedMap
        (\i section ->
            if section.keep then
                Maybe.map2 (\a b -> b.time - a.time) (at i draft.breakpoints) (at (i + 1) draft.breakpoints) |> Maybe.withDefault 0

            else
                0
        )
        draft.sections
        |> List.sum


navigation : Model -> Draft -> Html Msg
navigation model draft =
    let
        ( ( previousSection, nextSection ), ( previousBoundary, nextBoundary ) ) =
            case model.selection of
                Just (Boundary index) ->
                    ( ( index - 1, index ), ( index - 1, index + 1 ) )

                Just (Passage index) ->
                    ( ( index - 1, index + 1 ), ( index, index + 1 ) )

                Nothing ->
                    ( ( 0, 0 ), ( 1, 1 ) )

        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing
    in
    div [ class "editor__navigation" ]
        [ div [ Ui.sermonActions ]
            [ Button.action "ph:caret-left" "Previous section" False [ onClick (Select (Passage previousSection)), disabled (busy || previousSection < 0) ]
            , Button.action "ph:caret-right" "Next section" False [ onClick (Select (Passage nextSection)), disabled (busy || nextSection >= List.length draft.sections) ]
            , Button.action "ph:skip-back" "Previous breakpoint" False [ onClick (Select (Boundary previousBoundary)), disabled (busy || previousBoundary < 1) ]
            , Button.action "ph:skip-forward" "Next breakpoint" False [ onClick (Select (Boundary nextBoundary)), disabled (busy || nextBoundary >= List.length draft.breakpoints - 1) ]
            , (if model.addingBreakpoint then
                Button.action

               else
                Button.primaryAction
              )
                (if model.addingBreakpoint then
                    "ph:x"

                 else
                    "ph:plus"
                )
                (if model.addingBreakpoint then
                    "Cancel adding breakpoint"

                 else
                    "Add breakpoint"
                )
                False
                [ onClick ToggleAddingBoundary, disabled busy ]
            ]
        ]


selectedCard : Model -> Draft -> Html Msg
selectedCard model draft =
    case model.selection of
        Just (Passage index) ->
            case at index draft.sections of
                Just section ->
                    sectionCard model draft index section

                Nothing ->
                    text ""

        Just (Boundary index) ->
            case at index draft.breakpoints of
                Just b ->
                    boundaryPanel model draft index b

                Nothing ->
                    text ""

        Nothing ->
            text ""


sectionCard : Model -> Draft -> Int -> Section -> Html Msg
sectionCard model draft index section =
    let
        range =
            Maybe.map2 (\a b -> timestamp a.time ++ "–" ++ timestamp b.time) (at index draft.breakpoints) (at (index + 1) draft.breakpoints) |> Maybe.withDefault ""

        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing
    in
    Card.viewWithAttributes
        (if section.keep then
            []

         else
            [ Ui.deletedPanel ]
        )
        (text ("Section " ++ String.fromInt (index + 1)))
        (Just (text range))
        [ Button.primaryAction "ph:headphones" "Preview start & finish" False [ onClick Preview, disabled busy ]
        , if section.keep then
            Button.labeled "Delete" Button.deleteSection False [ onClick (Keep index False), disabled busy ]

          else
            Button.labeled "Keep" Button.keepSection False [ onClick (Keep index True), disabled busy ]
        ]
        []


boundaryPanel : Model -> Draft -> Int -> Breakpoint -> Html Msg
boundaryPanel model draft index b =
    let
        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing

        keyboard =
            preventDefaultOn "keydown"
                (Decode.map2
                    (\key shift ->
                        ( Nudge
                            (if key == "ArrowLeft" then
                                if shift then
                                    -1

                                else
                                    -0.1

                             else if shift then
                                1

                             else
                                0.1
                            )
                        , True
                        )
                    )
                    (Decode.field "key" Decode.string
                        |> Decode.andThen
                            (\key ->
                                if not busy && (key == "ArrowLeft" || key == "ArrowRight") then
                                    Decode.succeed key

                                else
                                    Decode.fail "not a nudge"
                            )
                    )
                    (Decode.field "shiftKey" Decode.bool)
                )
    in
    Card.viewWithAttributes
        [ id "breakpoint-adjustment", attribute "tabindex" "-1", attribute "aria-label" "Adjust breakpoint", keyboard ]
        (text (kindLabel b.kind))
        (Just (text (timestamp b.time)))
        [ div [ class "editor__breakpoint-actions" ]
            [ Button.primaryAction "ph:headphones" "Listen around breakpoint" False [ onClick Preview, disabled busy ]
            , Button.textAction "−0.1s" "Earlier by 0.1 seconds" False [ onClick (Nudge -0.1), disabled busy ]
            , Button.textAction "+0.1s" "Later by 0.1 seconds" False [ onClick (Nudge 0.1), disabled busy ]
            , Button.dangerAction "ph:trash" "Remove breakpoint" False [ onClick RemoveBoundary, disabled (busy || index == 0 || index == List.length draft.breakpoints - 1) ]
            ]
        ]
        []
