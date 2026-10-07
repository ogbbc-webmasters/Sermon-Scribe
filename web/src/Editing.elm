module Editing exposing (Model, Msg(..), init, isOpen, update, view)

import Browser.Dom
import Html exposing (Html, audio, button, div, h2, input, label, p, span, strong, text)
import Html.Attributes exposing (attribute, checked, class, controls, disabled, id, name, src, type_)
import Html.Events exposing (on, onClick, preventDefaultOn)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Process
import Task
import Ui
import Url


type alias Breakpoint =
    { id : String, time : Float, kind : String }


type alias Section =
    { id : String, keep : Bool }


type alias Draft =
    { duration : Float, revision : Int, breakpoints : List Breakpoint, sections : List Section }


type Selection
    = Boundary Int
    | Passage Int


type alias Model =
    { sermonId : Maybe String
    , draft : Maybe Draft
    , selection : Maybe Selection
    , saved : Maybe Draft
    , saving : Bool
    , applying : Bool
    , pendingApply : Maybe Bool
    , error : Maybe String
    , audioStatus : String
    , playhead : Float
    , undo : List Draft
    , generation : Int
    , sequence : Int
    }


init : Model
init =
    Model Nothing Nothing Nothing Nothing False False Nothing Nothing "" 0 [] 0 0


type Msg
    = Open String Bool
    | Loaded Int (Result Http.Error Draft)
    | Close
    | Select Selection
    | Keep Int Bool
    | Nudge Float
    | AddBoundary
    | RemoveBoundary
    | Undo
    | SaveLater Int
    | RetrySave
    | Saved Int (Result Http.Error Draft)
    | Apply Bool
    | Applied Int (Result Http.Error ())
    | Reload
    | Preview
    | PlayFull
    | Playhead Float
    | AudioStatus String
    | Focused (Result Browser.Dom.Error ())


isOpen : String -> Model -> Bool
isOpen sermonId model =
    model.sermonId == Just sermonId


endpoint : Model -> String
endpoint model =
    "/api/sermons/" ++ Url.percentEncode (Maybe.withDefault "" model.sermonId) ++ "/editing"


draftDecoder : Decode.Decoder Draft
draftDecoder =
    Decode.map4 Draft
        (Decode.field "duration" Decode.float)
        (Decode.field "revision" Decode.int)
        (Decode.field "breakpoints" (Decode.list (Decode.map3 Breakpoint (Decode.field "id" Decode.string) (Decode.field "time" Decode.float) (Decode.field "kind" Decode.string))))
        (Decode.field "sections" (Decode.list (Decode.map2 Section (Decode.field "id" Decode.string) (Decode.field "keep" Decode.bool))))


encodeDraft : Draft -> Encode.Value
encodeDraft draft =
    Encode.object
        [ ( "duration", Encode.float draft.duration )
        , ( "revision", Encode.int draft.revision )
        , ( "breakpoints", Encode.list (\b -> Encode.object [ ( "id", Encode.string b.id ), ( "time", Encode.float b.time ), ( "kind", Encode.string b.kind ) ]) draft.breakpoints )
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
        Open sermonId skip ->
            let
                next =
                    { init
                        | sermonId = Just sermonId
                        , generation = model.generation + 1
                        , pendingApply =
                            if skip then
                                Just True

                            else
                                Nothing
                    }
            in
            ( next, Http.get { url = endpoint next, expect = Http.expectJson (Loaded next.generation) draftDecoder }, stop )

        Loaded generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok draft ->
                        let
                            next =
                                { model | draft = Just draft, saved = Just draft, error = Nothing }
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
            if model.saving || model.applying || model.draft /= model.saved then
                ( model, Cmd.none, Nothing )

            else
                ( { init | generation = model.generation + 1 }, Cmd.none, stop )

        Reload ->
            update (Open (Maybe.withDefault "" model.sermonId) False) model

        Select selection ->
            ( { model | selection = Just selection, audioStatus = "", playhead = sectionStart selection model }
            , case selection of
                Boundary _ ->
                    Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment")

                Passage _ ->
                    Cmd.none
            , stop
            )

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
                                    change (\d -> { d | breakpoints = replaceAt index (\b -> { b | time = time }) d.breakpoints }) model
                            in
                            ( next, cmd, preview next )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        AddBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Passage index) ) ->
                    case ( at index draft.breakpoints, at (index + 1) draft.breakpoints, at index draft.sections ) of
                        ( Just before, Just after, Just section ) ->
                            if model.playhead - before.time < 0.05 || after.time - model.playhead < 0.05 then
                                ( { model | error = Just "Play or seek inside the section before adding a breakpoint." }, Cmd.none, Nothing )

                            else
                                let
                                    unique =
                                        "manual-" ++ String.fromInt draft.revision ++ "-" ++ String.fromInt model.sequence

                                    ( next, cmd, audioEffect ) =
                                        change
                                            (\d ->
                                                { d
                                                    | breakpoints = List.take (index + 1) d.breakpoints ++ [ Breakpoint unique model.playhead "manual" ] ++ List.drop (index + 1) d.breakpoints
                                                    , sections = List.take (index + 1) d.sections ++ [ Section (unique ++ "-section") section.keep ] ++ List.drop (index + 1) d.sections
                                                }
                                            )
                                            model
                                in
                                ( { next | selection = Just (Boundary (index + 1)) }, Cmd.batch [ cmd, Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment") ], audioEffect )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        RemoveBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Boundary index) ) ->
                    case ( at (index - 1) draft.sections, at index draft.sections ) of
                        ( Just left, Just right ) ->
                            if left.keep /= right.keep then
                                ( { model | error = Just "Choose the same Keep / Delete setting on both sections before joining them." }, Cmd.none, Nothing )

                            else
                                let
                                    ( next, cmd, audioEffect ) =
                                        change (\d -> { d | breakpoints = removeAt index d.breakpoints, sections = removeAt index d.sections }) model
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
            if model.draft /= model.saved || model.saving then
                save { model | pendingApply = Just skip }

            else
                apply skip model

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
            if model.applying || model.pendingApply /= Nothing || transform draft == draft then
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
            if model.saving || model.applying || model.draft == model.saved then
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
            ( { model | applying = True, pendingApply = Nothing, error = Nothing }
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
            "New speaker"

        "singing_start" ->
            "Singing starts"

        "singing_end" ->
            "Singing ends"

        _ ->
            "Manual breakpoint"


view : Model -> Html Msg
view model =
    div [ class "editor" ]
        [ h2 [ Ui.panelHeading ] [ text "Edit recording" ]
        , p [ Ui.hint ] [ text "Listen to the edges. Keep what belongs; delete what doesn't. Your source recording stays untouched." ]
        , case model.error of
            Just error ->
                div [ Ui.errorPanel, attribute "role" "alert" ]
                    [ p [] [ text error ]
                    , div [ Ui.sermonActions ]
                        [ if model.draft == Nothing then
                            button [ Ui.button, onClick Close ] [ text "Close editor" ]

                          else
                            button [ Ui.button, onClick RetrySave, disabled model.saving ] [ text "Try saving again" ]
                        , button [ Ui.button, onClick Reload, disabled (model.saving || model.applying) ] [ text "Reload saved draft" ]
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
                    [ div [ class "editor__toolbar" ]
                        [ button [ Ui.button, onClick Undo, disabled (List.isEmpty model.undo || model.applying || model.pendingApply /= Nothing) ] [ text "Undo" ]
                        , span [ attribute "role" "status" ]
                            [ text
                                (if model.saving then
                                    "Saving draft…"

                                 else if model.draft /= model.saved then
                                    "Draft not saved"

                                 else
                                    "Draft saved"
                                )
                            ]
                        , button [ Ui.button, onClick Close, disabled (model.saving || model.applying || model.draft /= model.saved) ] [ text "Close editor" ]
                        ]
                    , div [ class "editor__list" ] (List.concat (List.indexedMap (sectionRow model draft) draft.sections))
                    , div [ class "editor__footer" ]
                        [ strong [] [ text ("Kept duration: " ++ timestamp (keptDuration draft)) ]
                        , button [ Ui.primaryButton, onClick (Apply False), disabled (model.applying || model.pendingApply /= Nothing || not (List.any .keep draft.sections) || model.error /= Nothing) ]
                            [ text
                                (if model.applying || model.pendingApply /= Nothing then
                                    "Applying…"

                                 else
                                    "Apply edits & continue"
                                )
                            ]
                        , p [ Ui.hint ] [ text "Metadata will be generated from the kept sections. You can reopen editing later." ]
                        ]
                    ]
        ]


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


sectionRow : Model -> Draft -> Int -> Section -> List (Html Msg)
sectionRow model draft index section =
    let
        range =
            Maybe.map2 (\a b -> timestamp a.time ++ "–" ++ timestamp b.time) (at index draft.breakpoints) (at (index + 1) draft.breakpoints) |> Maybe.withDefault ""

        busy =
            model.applying || model.pendingApply /= Nothing

        radios =
            div [ class "editor__choices", attribute "role" "group", attribute "aria-label" ("Section " ++ String.fromInt (index + 1) ++ " inclusion") ]
                (List.map (\( keep, caption ) -> label [ class "editor__choice" ] [ input [ type_ "radio", name section.id, checked (section.keep == keep), disabled busy, onClick (Keep index keep) ] [], text caption ]) [ ( True, "Keep" ), ( False, "Delete" ) ])

        boundary =
            case at (index + 1) draft.breakpoints of
                Just b ->
                    if b.kind == "end" then
                        []

                    else
                        [ button
                            [ class "editor__breakpoint"
                            , onClick (Select (Boundary (index + 1)))
                            , disabled busy
                            , attribute "aria-expanded"
                                (if model.selection == Just (Boundary (index + 1)) then
                                    "true"

                                 else
                                    "false"
                                )
                            ]
                            [ text (timestamp b.time ++ " · " ++ kindLabel b.kind) ]
                        , if model.selection == Just (Boundary (index + 1)) then
                            boundaryPanel model draft (index + 1) b

                          else
                            text ""
                        ]

                Nothing ->
                    []
    in
    [ div
        [ class
            (if section.keep then
                "editor__section"

             else
                "editor__section editor__section--deleted"
            )
        ]
        [ div [ class "editor__row" ]
            [ button
                [ class "editor__section-title"
                , onClick (Select (Passage index))
                , disabled busy
                , attribute "aria-expanded"
                    (if model.selection == Just (Passage index) then
                        "true"

                     else
                        "false"
                    )
                ]
                [ strong [] [ text ("Section " ++ String.fromInt (index + 1)) ]
                , span [] [ text range ]
                , if section.keep then
                    text ""

                  else
                    span [] [ text "Excluded" ]
                ]
            , radios
            ]
        , if model.selection == Just (Passage index) then
            sectionPanel model draft index

          else
            text ""
        ]
    ]
        ++ boundary


boundaryPanel : Model -> Draft -> Int -> Breakpoint -> Html Msg
boundaryPanel model draft index b =
    let
        busy =
            model.applying || model.pendingApply /= Nothing

        removable =
            Maybe.map2 (\left right -> left.keep == right.keep) (at (index - 1) draft.sections) (at index draft.sections) |> Maybe.withDefault False

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
    div [ class "editor__adjustment", id "breakpoint-adjustment", attribute "tabindex" "-1", attribute "aria-label" "Adjust breakpoint", keyboard ]
        [ strong [] [ text ("Place breakpoint · " ++ String.fromFloat (toFloat (round (b.time * 10)) / 10) ++ " seconds") ]
        , p [ Ui.hint ] [ text "One second before → high beep → one second after." ]
        , div [ Ui.sermonActions ]
            [ button [ Ui.button, onClick Preview, disabled busy ] [ text "Listen around breakpoint" ]
            , button [ Ui.button, onClick (Nudge -0.1), disabled busy ] [ text "← Earlier" ]
            , button [ Ui.button, onClick (Nudge 0.1), disabled busy ] [ text "Later →" ]
            ]
        , p [ Ui.hint ] [ text "Arrow keys move 0.1 seconds and replay. Shift + arrow moves 1 second." ]
        , p [ attribute "role" "status", Ui.hint ] [ text model.audioStatus ]
        , button [ Ui.button, onClick RemoveBoundary, disabled (busy || not removable) ] [ text "Remove breakpoint" ]
        , if removable then
            text ""

          else
            p [ Ui.hint ] [ text "To join these sections, first give both the same Keep / Delete choice." ]
        ]


sectionPanel : Model -> Draft -> Int -> Html Msg
sectionPanel model draft index =
    div [ class "editor__adjustment" ]
        [ p [ Ui.hint ] [ text "First three seconds → low tone (middle skipped) → last three seconds. Short sections play in full." ]
        , div [ Ui.sermonActions ]
            [ button [ Ui.button, onClick Preview ] [ text "Preview start & finish" ]
            , button [ Ui.button, onClick PlayFull ] [ text "Play full section" ]
            ]
        , audio [ id "editing-source", class "editor__audio", controls True, attribute "preload" "metadata", attribute "data-start" (at index draft.breakpoints |> Maybe.map (.time >> String.fromFloat) |> Maybe.withDefault "0"), attribute "data-end" (at (index + 1) draft.breakpoints |> Maybe.map (.time >> String.fromFloat) |> Maybe.withDefault "0"), src ("/api/sermons/" ++ Url.percentEncode (Maybe.withDefault "" model.sermonId) ++ "/audio/source"), on "timeupdate" (Decode.at [ "target", "currentTime" ] Decode.float |> Decode.map Playhead) ] []
        , p [ attribute "role" "status", Ui.hint ] [ text model.audioStatus ]
        , div [ Ui.sermonActions ]
            [ button [ Ui.button, onClick AddBoundary, disabled (model.applying || model.pendingApply /= Nothing) ] [ text ("Add breakpoint at " ++ timestamp model.playhead) ]
            , button [ Ui.button, onClick (Select (Boundary index)), disabled (index == 0) ] [ text "Adjust start" ]
            , button [ Ui.button, onClick (Select (Boundary (index + 1))), disabled (index + 1 == List.length draft.breakpoints - 1) ] [ text "Adjust finish" ]
            ]
        ]
